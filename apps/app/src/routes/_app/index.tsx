import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import {
  ArrowUpRightIcon,
  BrainIcon,
  CheckCheckIcon,
  CheckIcon,
  ChevronRightIcon,
  CopyIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react"
import { z } from "zod"

import { FactDates } from "@/components/fact-dates"
import { DeleteMemoryDialog, EditMemoryDialog, editableDates, type EditableMemory } from "@/components/memory-dialogs"
import { PageHeader } from "@/components/page-header"
import { PersonLinks } from "@/components/person-links"
import { VersionChain } from "@/components/version-chain"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { PagePending, PageHeaderSkeleton, TableSkeleton } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { Toggle } from "@/components/ui/toggle"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { getMemories, resolveMemory } from "@/lib/fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"
import { useMinWidth } from "@/hooks/use-min-width"

export const Route = createFileRoute("/_app/")({
  // ?review=true lists only facts flagged as conflicting with another fact.
  // ?m= opens one fact's history and scrolls to it (Ask links here).
  // ?q= and ?kind= filter the loaded facts in the browser.
  validateSearch: z.object({
    review: z.boolean().optional(),
    m: z.string().max(64).optional(),
    q: z.string().max(200).optional(),
    kind: z.enum(["static", "dynamic"]).optional(),
  }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c, review: search.review ?? false }),
  loader: ({ deps, context }) => getMemories({ data: { container: deps.c ?? context.defaultContainer, review: deps.review } }),
  pendingComponent: MemoriesSkeleton,
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Memories · Recall" }] }),
  component: MemoriesPage,
})

type Row = Awaited<ReturnType<typeof getMemories>>["memories"][number]
type Kind = "all" | "static" | "dynamic"

function MemoriesPage() {
  const container = useContainer()
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const review = search.review ?? false
  const kind: Kind = search.kind ?? "all"
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const [extra, setExtra] = React.useState<Row[]>([])
  const [cursor, setCursor] = React.useState(data.nextCursor)
  const [loadingMore, setLoadingMore] = React.useState(false)
  const [filter, setFilter] = React.useState(search.q ?? "")
  const filterRef = React.useRef<HTMLInputElement>(null)
  const clearFilter = () => {
    setFilter("")
    filterRef.current?.focus()
  }
  const [open, setOpen] = React.useState<string | null>(null)
  // Columns on screen (Added from sm, History from md, Source from lg). Spanning
  // more than that adds empty columns that squeeze the fact on a phone.
  const columns = 3 + Number(useMinWidth(640)) + Number(useMinWidth(768)) + Number(useMinWidth(1024))
  const [editing, setEditing] = React.useState<EditableMemory | null>(null)
  const [deleting, setDeleting] = React.useState<{ id: string; fact: string; keep?: string } | null>(null)

  const focus = search.m
  // A linked fact that isn't in the loaded page (or isn't current anymore).
  const [pinned, setPinned] = React.useState<string | null>(null)

  // A new container or a refresh replaces the first page.
  React.useEffect(() => {
    setExtra([])
    setCursor(data.nextCursor)
    setOpen(null)
  }, [data])

  // ?m= (from Ask's citations) opens that fact once, then leaves the URL, so
  // a refresh or an edit doesn't jump back to it.
  React.useEffect(() => {
    if (!focus) return
    if (data.memories.some((m) => m.id === focus)) {
      setOpen(focus)
      setPinned(null)
      requestAnimationFrame(() => document.getElementById(`memory-${focus}`)?.scrollIntoView({ block: "center" }))
    } else {
      setPinned(focus)
    }
    void navigate({ search: (prev) => ({ ...prev, m: undefined }), replace: true })
  }, [focus, data, navigate])

  // The URL follows the field a beat behind typing, as on People.
  React.useEffect(() => {
    const next = filter.trim() || undefined
    if (next === search.q) return
    const t = setTimeout(() => void navigate({ search: (prev) => ({ ...prev, q: next }), replace: true }), 200)
    return () => clearTimeout(t)
  }, [filter, search.q, navigate])
  const setKind = (k: Kind) => void navigate({ search: (prev) => ({ ...prev, kind: k === "all" ? undefined : k }), replace: true })

  const all = [...data.memories, ...extra]
  const q = filter.trim().toLowerCase()
  const rows = all.filter(
    (m) => (kind === "all" || m.kind === kind) && (!q || m.fact.toLowerCase().includes(q) || m.keywords.toLowerCase().includes(q)),
  )
  const counts = { static: all.filter((m) => m.kind === "static").length, dynamic: all.filter((m) => m.kind === "dynamic").length }

  const loadMore = async () => {
    if (!cursor) return
    setLoadingMore(true)
    try {
      const next = await getMemories({ data: { container, cursor, review } })
      setExtra((e) => [...e, ...next.memories])
      setCursor(next.nextCursor)
    } catch (err) {
      toast.add({ title: "Couldn't load more", description: errorMessage(err), type: "error" })
    } finally {
      setLoadingMore(false)
    }
  }

  const refresh = () => void router.invalidate()
  const setReview = (on: boolean) => void navigate({ search: (prev) => ({ ...prev, review: on || undefined }), replace: true })
  const resolve = async (id: string) => {
    try {
      await resolveMemory({ data: { id } })
      toast.add({ title: "Marked as resolved", type: "success" })
      refresh()
    } catch (err) {
      toast.add({ title: "Couldn't mark it resolved", description: errorMessage(err), type: "error" })
    }
  }
  const copyId = async (id: string) => {
    try {
      await navigator.clipboard.writeText(id)
      toast.add({ title: "Copied the memory id", type: "success" })
    } catch {
      toast.add({ title: "Copy failed", description: id, type: "error" })
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Memories"
        description={
          <>
            Current facts about <code>{container}</code>.
          </>
        }
        actions={
          <Button render={<Link to="/add" search={{ c: container }} />}>
            <PlusIcon data-icon="inline-start" />
            Add content
          </Button>
        }
      />

      {pinned && (
        <Card className="enter-in">
          <CardHeader>
            <CardTitle>Linked fact</CardTitle>
            <CardAction>
              <Button variant="ghost" size="sm" onClick={() => setPinned(null)}>
                Dismiss
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            <VersionChain id={pinned} />
          </CardContent>
        </Card>
      )}

      {(data.profile.static || data.profile.dynamic) && <ProfileSummary profile={data.profile} />}

      {all.length === 0 && review ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CheckCheckIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing needs review</EmptyTitle>
          </EmptyHeader>
          <EmptyContent className="flex-row justify-center">
            <Button variant="outline" onClick={() => setReview(false)}>
              Show all facts
            </Button>
          </EmptyContent>
        </Empty>
      ) : all.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BrainIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing remembered here yet</EmptyTitle>
            <EmptyDescription>
              Add some text, a URL or a file to <code>{container}</code>, or point an agent at{" "}
              <code>POST /v1/memories</code> or the <code>/mcp</code> endpoint.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row justify-center">
            <Button render={<Link to="/add" search={{ c: container }} />}>
              <PlusIcon data-icon="inline-start" />
              Add content
            </Button>
            <Button variant="outline" render={<Link to="/settings/keys" />}>
              Get an API key
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <section aria-label="Current facts" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <InputGroup className="w-full sm:w-72">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput ref={filterRef} aria-label="Filter facts" placeholder="Filter facts and keywords…" value={filter} onChange={(e) => setFilter(e.target.value)} />
              {filter && (
                <InputGroupAddon align="inline-end">
                  <InputGroupButton size="icon-xs" aria-label="Clear filter" onClick={clearFilter}>
                    <XIcon />
                  </InputGroupButton>
                </InputGroupAddon>
              )}
            </InputGroup>
            <ToggleGroup
              aria-label="Kind"
              variant="outline"
              value={[kind]}
              onValueChange={(v: string[]) => v[0] && setKind(v[0] as Kind)}
            >
              <ToggleGroupItem value="all">All</ToggleGroupItem>
              <ToggleGroupItem value="dynamic">Dynamic {counts.dynamic}</ToggleGroupItem>
              <ToggleGroupItem value="static">Static {counts.static}</ToggleGroupItem>
            </ToggleGroup>
            {(data.reviewCount > 0 || review) && (
              <Toggle variant="outline" pressed={review} onPressedChange={setReview}>
                <TriangleAlertIcon data-icon="inline-start" />
                Needs review <span className="tabular-nums">{data.reviewCount}</span>
              </Toggle>
            )}
            <p className="ms-auto text-caption text-muted-foreground tabular-nums" role="status">
              {rows.length === all.length ? plural(all.length, "fact") : `${rows.length} of ${plural(all.length, "fact")}`}
              {cursor ? " loaded" : ""}
            </p>
          </div>

          <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  {/* 14px inset + the 28px button (36px on touch); the button's own 6px padding puts the chevron glyph on the card's 20px edge. */}
                  <TableHead className="w-11 pe-0 first:ps-3.5 pointer-coarse:w-13" aria-label="Expand" />
                  <TableHead>Fact</TableHead>
                  <TableHead className="hidden w-28 md:table-cell">History</TableHead>
                  <TableHead className="hidden w-56 lg:table-cell">Source</TableHead>
                  <TableHead className="hidden w-28 sm:table-cell">Added</TableHead>
                  <TableHead className="w-12 ps-0 pointer-coarse:w-14" aria-label="Actions" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={columns} className="py-10 whitespace-normal">
                      <NoMatch
                        q={q ? filter.trim() : ""}
                        kind={kind}
                        loaded={cursor ? all.length : null}
                        container={container}
                        onClear={() => (q ? clearFilter() : setKind("all"))}
                        onLoadMore={() => void loadMore()}
                      />
                    </TableCell>
                  </TableRow>
                )}
                {rows.map((m) => {
                  const expanded = open === m.id
                  return (
                    <React.Fragment key={m.id}>
                      <TableRow id={`memory-${m.id}`} data-state={expanded ? "selected" : undefined} className="scroll-mt-20 [&>td]:align-top">
                        <TableCell className="py-1.5 pe-0 first:ps-3.5">
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-expanded={expanded}
                            aria-controls={`chain-${m.id}`}
                            aria-label={expanded ? "Hide history" : "Show history"}
                            onClick={() => setOpen(expanded ? null : m.id)}
                          >
                            <ChevronRightIcon className={cn("transition-transform duration-200 ease-emphasized", expanded && "rotate-90")} />
                          </Button>
                        </TableCell>
                        <TableCell className="py-2.5 whitespace-normal">
                          <p className="text-sm text-pretty break-words">{m.fact}</p>
                          <div className="mt-1 flex flex-wrap items-center gap-1.5">
                            {m.reviewNote && (
                              <Badge variant="warning">
                                <TriangleAlertIcon />
                                Needs review
                              </Badge>
                            )}
                            {m.kind === "static" && <Badge variant="outline">Static</Badge>}
                            <FactDates memory={m} />
                            <PersonLinks people={m.people} container={container} />
                            {m.versions > 1 && (
                              <Badge variant="soft" className="md:hidden">
                                v{m.versions}
                              </Badge>
                            )}
                            {m.keywords && (
                              <span className="line-clamp-1 min-w-0 flex-[1_1_6rem] text-caption text-muted-foreground" title={m.keywords}>
                                {m.keywords}
                              </span>
                            )}
                          </div>
                          {m.reviewNote && (
                            <>
                              <p className="mt-1.5 text-caption text-pretty break-words whitespace-pre-line text-warning-fg">{m.reviewNote}</p>
                              {/* The queue's job is to settle the pair, so the choices sit on the row, not in its menu. */}
                              <div className="mt-2 flex flex-wrap items-center gap-2">
                                {m.conflictWith?.current && (
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    aria-label={`Keep this one: “${m.fact.slice(0, 60)}”. Deletes the other fact`}
                                    onClick={() => setDeleting({ id: m.conflictWith!.id, fact: m.conflictWith!.fact, keep: m.fact })}
                                  >
                                    <CheckIcon data-icon="inline-start" />
                                    Keep this one
                                  </Button>
                                )}
                                <Button variant="outline" size="sm" onClick={() => void resolve(m.id)}>
                                  <CheckCheckIcon data-icon="inline-start" />
                                  {m.conflictWith?.current ? "Both are right" : "Mark resolved"}
                                </Button>
                                {m.conflictWith && (
                                  <Button
                                    variant="link"
                                    size="caption"
                                    render={<Link to="/" search={(prev) => ({ ...prev, m: m.conflictWith!.id })} />}
                                  >
                                    {m.conflictWith.current ? "Go to the other fact" : "See the other fact's history"}
                                    <ArrowUpRightIcon data-icon="inline-end" />
                                  </Button>
                                )}
                              </div>
                            </>
                          )}
                        </TableCell>
                        <TableCell className="hidden py-2.5 md:table-cell">
                          {m.versions > 1 ? (
                            <Button
                              variant="link"
                              size="caption"
                              aria-expanded={expanded}
                              aria-controls={`chain-${m.id}`}
                              onClick={() => setOpen(expanded ? null : m.id)}
                            >
                              {m.versions} versions
                            </Button>
                          ) : (
                            <span className="text-caption text-muted-foreground">Original</span>
                          )}
                        </TableCell>
                        <TableCell className="hidden truncate py-2.5 lg:table-cell">
                          <span className="text-caption text-muted-foreground" title={m.sourceTitle ?? undefined}>
                            {m.sourceTitle ?? (m.sourceDocumentId ? "Deleted document" : "Added by hand")}
                          </span>
                        </TableCell>
                        <TableCell className="hidden py-2.5 sm:table-cell">
                          <time
                            className="text-caption text-muted-foreground tabular-nums"
                            dateTime={new Date(m.createdAt).toISOString()}
                            title={fullDate(m.createdAt)}
                          >
                            {ago(m.createdAt)}
                          </time>
                        </TableCell>
                        <TableCell className="py-1.5 ps-0 text-end">
                          <DropdownMenu>
                            <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for “${m.fact.slice(0, 40)}”`} />}>
                              <MoreHorizontalIcon />
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="w-44">
                              <DropdownMenuGroup>
                                <DropdownMenuItem onSelect={() => setEditing({ id: m.id, fact: m.fact, keywords: m.keywords, kind: m.kind, ...editableDates(m) })}>
                                  <PencilIcon />
                                  Edit
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={() => void copyId(m.id)}>
                                  <CopyIcon />
                                  Copy id
                                </DropdownMenuItem>
                              </DropdownMenuGroup>
                              <DropdownMenuSeparator />
                              <DropdownMenuGroup>
                                <DropdownMenuItem variant="destructive" onSelect={() => setDeleting({ id: m.id, fact: m.fact })}>
                                  <Trash2Icon />
                                  Delete
                                </DropdownMenuItem>
                              </DropdownMenuGroup>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </TableCell>
                      </TableRow>
                      {expanded && (
                        <TableRow className="hover:bg-transparent">
                          <TableCell />
                          <TableCell colSpan={columns - 1} id={`chain-${m.id}`} className="enter-in pt-1 pb-4 whitespace-normal">
                            <VersionChain id={m.id} />
                          </TableCell>
                        </TableRow>
                      )}
                    </React.Fragment>
                  )
                })}
              </TableBody>
            </Table>
          </div>

          {cursor && (
            <Button variant="outline" className="self-center" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore && <Spinner data-icon="inline-start" />}
              Load more
            </Button>
          )}
        </section>
      )}

      <EditMemoryDialog
        memory={editing}
        onOpenChange={(o) => !o && setEditing(null)}
        onSaved={() => {
          toast.add({ title: "Saved as a new version", type: "success" })
          refresh()
        }}
      />
      <DeleteMemoryDialog
        memory={deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        onDeleted={() => {
          toast.add({ title: "Fact deleted", description: "It's still in the history.", type: "success" })
          refresh()
        }}
      />
    </div>
  )
}

function NoMatch({
  q,
  kind,
  loaded,
  container,
  onClear,
  onLoadMore,
}: {
  q: string
  kind: Kind
  loaded: number | null
  container: string
  onClear: () => void
  onLoadMore: () => void
}) {
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <p className="text-sm text-pretty wrap-anywhere text-muted-foreground">
        {q ? `No fact matches “${q}”` : `No ${kind} facts`}
        {loaded !== null && ` in the ${loaded.toLocaleString("en")} loaded`}
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        <Button variant="outline" size="sm" onClick={onClear}>
          {q ? "Clear filter" : "Show all"}
        </Button>
        {loaded !== null && (
          <Button variant="outline" size="sm" onClick={onLoadMore}>
            Load more
          </Button>
        )}
        {q && (
          <Button variant="ghost" size="sm" render={<Link to="/search" search={{ c: container, q }} />}>
            <SearchIcon data-icon="inline-start" />
            Search by meaning
          </Button>
        )}
      </div>
    </div>
  )
}

function ProfileSummary({ profile }: { profile: { static: string; dynamic: string; updatedAt: number | null } }) {
  return (
    <Card role="region" aria-labelledby="profile-heading">
      <CardHeader>
        <CardTitle id="profile-heading">Profile</CardTitle>
        {profile.updatedAt && (
          <CardAction className="text-caption text-muted-foreground" title={fullDate(profile.updatedAt)}>
            Updated {ago(profile.updatedAt)}
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
      <dl className="grid gap-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1">
          <dt className="text-caption font-medium text-muted-foreground">Who</dt>
          <dd className="text-sm text-pretty">{profile.static || <span className="text-muted-foreground">No lasting traits yet.</span>}</dd>
        </div>
        <div className="flex flex-col gap-1">
          <dt className="text-caption font-medium text-muted-foreground">Right now</dt>
          <dd className="text-sm text-pretty">{profile.dynamic || <span className="text-muted-foreground">Nothing current yet.</span>}</dd>
        </div>
      </dl>
      </CardContent>
    </Card>
  )
}

function MemoriesSkeleton() {
  return (
    <PagePending label="Loading memories…">
      <PageHeaderSkeleton description="w-80" action />
      <div className="flex items-center gap-2">
        <Skeleton className="h-8 w-full max-w-72" />
        <Skeleton className="h-8 w-10" />
        <Skeleton className="h-8 w-20" />
        <Skeleton className="h-8 w-16" />
      </div>
      <TableSkeleton
        columns={[
          { className: "w-11", short: "size-4", bare: true },
          { sub: true },
          { className: "hidden w-28 md:table-cell", short: "w-14" },
          { className: "hidden w-56 lg:table-cell", short: "w-24" },
          { className: "hidden w-28 sm:table-cell", short: "w-20" },
          { className: "w-12", short: "size-4", bare: true },
        ]}
      />
    </PagePending>
  )
}

