import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { BrainIcon, ChevronRightIcon, CopyIcon, MoreHorizontalIcon, PencilIcon, PlusIcon, SearchIcon, Trash2Icon } from "lucide-react"

import { DeleteMemoryDialog, EditMemoryDialog, type EditableMemory } from "@/components/memory-dialogs"
import { PageHeader } from "@/components/page-header"
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
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { getMemories } from "@/lib/fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"
import { z } from "zod"

export const Route = createFileRoute("/_app/")({
  // ?m= opens one fact's history and scrolls to it (Ask links here).
  validateSearch: z.object({ m: z.string().max(64).optional() }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c }),
  loader: ({ deps, context }) => getMemories({ data: { container: deps.c ?? context.defaultContainer } }),
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
  const router = useRouter()
  const [extra, setExtra] = React.useState<Row[]>([])
  const [cursor, setCursor] = React.useState(data.nextCursor)
  const [loadingMore, setLoadingMore] = React.useState(false)
  const [filter, setFilter] = React.useState("")
  const [kind, setKind] = React.useState<Kind>("all")
  const [open, setOpen] = React.useState<string | null>(null)
  const [editing, setEditing] = React.useState<EditableMemory | null>(null)
  const [deleting, setDeleting] = React.useState<{ id: string; fact: string } | null>(null)

  const { m: focus } = Route.useSearch()

  // A new container or a refresh replaces the first page.
  React.useEffect(() => {
    setExtra([])
    setCursor(data.nextCursor)
    setOpen(focus && data.memories.some((m) => m.id === focus) ? focus : null)
  }, [data, focus])

  React.useEffect(() => {
    if (!focus) return
    document.getElementById(`memory-${focus}`)?.scrollIntoView({ block: "center" })
  }, [focus, data])

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
      const next = await getMemories({ data: { container, cursor } })
      setExtra((e) => [...e, ...next.memories])
      setCursor(next.nextCursor)
    } catch (err) {
      toast.add({ title: "Couldn't load more", description: errorMessage(err), type: "error" })
    } finally {
      setLoadingMore(false)
    }
  }

  const refresh = () => void router.invalidate()
  const copyId = async (id: string) => {
    try {
      await navigator.clipboard.writeText(id)
      toast.add({ title: "Copied the memory id", type: "success" })
    } catch {
      toast.add({ title: "Copy failed", description: id, type: "error" })
    }
  }

  return (
    <div className="page-in flex flex-col gap-6">
      <PageHeader
        title="Memories"
        description={
          <>
            What Recall believes about <span className="font-mono break-all text-foreground">{container}</span> right now. Facts that changed show how
            many versions they've had.
          </>
        }
        actions={
          <Button render={<Link to="/add" search={{ c: container }} />}>
            <PlusIcon data-icon="inline-start" />
            Add content
          </Button>
        }
      />

      {(data.profile.static || data.profile.dynamic) && <ProfileSummary profile={data.profile} />}

      {all.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BrainIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing remembered here yet</EmptyTitle>
            <EmptyDescription>
              Add some text, a URL or a file to <span className="font-mono break-all">{container}</span>, or point an agent at{" "}
              <code>POST /v1/memories</code> or the <code>/mcp</code> endpoint.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row justify-center">
            <Button render={<Link to="/add" search={{ c: container }} />}>
              <PlusIcon data-icon="inline-start" />
              Add content
            </Button>
            <Button variant="outline" render={<Link to="/keys" />}>
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
              <InputGroupInput aria-label="Filter facts" placeholder="Filter facts and keywords" value={filter} onChange={(e) => setFilter(e.target.value)} />
            </InputGroup>
            <ToggleGroup
              aria-label="Kind"
              variant="outline"
              size="sm"
              value={[kind]}
              onValueChange={(v: string[]) => v[0] && setKind(v[0] as Kind)}
            >
              <ToggleGroupItem value="all">All</ToggleGroupItem>
              <ToggleGroupItem value="dynamic">Dynamic {counts.dynamic}</ToggleGroupItem>
              <ToggleGroupItem value="static">Static {counts.static}</ToggleGroupItem>
            </ToggleGroup>
            <p className="ms-auto text-caption text-muted-foreground tabular-nums" role="status">
              {rows.length === all.length ? plural(all.length, "fact") : `${rows.length} of ${plural(all.length, "fact")}`}
              {cursor ? " loaded" : ""}
            </p>
          </div>

          <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-9" aria-label="Expand" />
                  <TableHead>Fact</TableHead>
                  <TableHead className="hidden w-28 md:table-cell">History</TableHead>
                  <TableHead className="hidden w-56 lg:table-cell">Source</TableHead>
                  <TableHead className="hidden w-28 sm:table-cell">Added</TableHead>
                  <TableHead className="w-12" aria-label="Actions" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={6} className="py-10 text-center text-sm text-muted-foreground">
                      {cursor ? `No match in the ${all.length} loaded facts. ` : `No fact matches “${filter}”. `}
                      {cursor && (
                        <>
                          <Button variant="link" className="h-auto p-0" onClick={() => void loadMore()}>
                            Load more
                          </Button>{" "}
                          or{" "}
                        </>
                      )}
                      <Button variant="link" className="h-auto p-0" render={<Link to="/search" search={{ c: container, q: filter }} />}>
                        Try a semantic search instead
                      </Button>
                    </TableCell>
                  </TableRow>
                )}
                {rows.map((m) => {
                  const expanded = open === m.id
                  return (
                    <React.Fragment key={m.id}>
                      <TableRow id={`memory-${m.id}`} data-state={expanded ? "selected" : undefined} className="scroll-mt-20 align-top">
                        <TableCell className="pe-0">
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
                            {m.kind === "static" && <Badge variant="outline">Static</Badge>}
                            {m.versions > 1 && (
                              <Badge variant="soft" className="md:hidden">
                                v{m.versions}
                              </Badge>
                            )}
                            {m.keywords && (
                              <span className="line-clamp-1 min-w-0 flex-1 text-caption text-muted-foreground" title={m.keywords}>
                                {m.keywords}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="hidden py-2.5 md:table-cell">
                          {m.versions > 1 ? (
                            <Button variant="link" className="h-auto p-0 text-caption" onClick={() => setOpen(expanded ? null : m.id)}>
                              {m.versions} versions
                            </Button>
                          ) : (
                            <span className="text-caption text-muted-foreground">Original</span>
                          )}
                        </TableCell>
                        <TableCell className="hidden py-2.5 lg:table-cell">
                          <span className="block truncate text-caption text-muted-foreground" title={m.sourceTitle ?? undefined}>
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
                        <TableCell className="py-1.5 text-end">
                          <DropdownMenu>
                            <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for “${m.fact.slice(0, 40)}”`} />}>
                              <MoreHorizontalIcon />
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="w-44">
                              <DropdownMenuGroup>
                                <DropdownMenuItem onClick={() => setEditing({ id: m.id, fact: m.fact, keywords: m.keywords, kind: m.kind })}>
                                  <PencilIcon />
                                  Edit
                                </DropdownMenuItem>
                                <DropdownMenuItem onClick={() => void copyId(m.id)}>
                                  <CopyIcon />
                                  Copy id
                                </DropdownMenuItem>
                              </DropdownMenuGroup>
                              <DropdownMenuSeparator />
                              <DropdownMenuGroup>
                                <DropdownMenuItem variant="destructive" onClick={() => setDeleting({ id: m.id, fact: m.fact })}>
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
                          <TableCell colSpan={5} id={`chain-${m.id}`} className="animate-[enter-in_200ms_var(--ease-emphasized)] pt-1 pb-4 whitespace-normal">
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
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading memories">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-4 w-80 max-w-full" />
      </div>
      <Skeleton className="h-28 w-full rounded-xl" />
      <div className="flex flex-col gap-px overflow-hidden rounded-xl shadow-(--shadow-border)">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-14 w-full rounded-none" />
        ))}
      </div>
    </div>
  )
}

