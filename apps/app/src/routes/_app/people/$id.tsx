import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import {
  ArrowLeftIcon,
  FileTextIcon,
  GitMergeIcon,
  HistoryIcon,
  MessageCircleQuestionMarkIcon,
  MoreHorizontalIcon,
  PencilIcon,
  Trash2Icon,
  TriangleAlertIcon,
  UserRoundXIcon,
} from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { DeletePersonDialog, EditPersonDialog, MergePersonDialog } from "@/components/person-dialogs"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Item, ItemContent, ItemGroup } from "@/components/ui/item"
import { Label } from "@/components/ui/label"
import { PagePending, PageHeaderSkeleton } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { toast } from "@/components/ui/toast"
import { useContainer } from "@/hooks/use-container"
import { getPersonPage } from "@/lib/fns"
import { fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/people/$id")({
  // ?history=true also lists facts that were replaced or deleted since.
  validateSearch: z.object({ history: z.boolean().optional() }),
  loaderDeps: ({ search }) => ({ history: search.history ?? false }),
  loader: ({ params, deps }) => getPersonPage({ data: { id: params.id, history: deps.history } }),
  pendingComponent: PersonSkeleton,
  errorComponent: AppError,
  head: ({ loaderData }) => ({ meta: [{ title: `${loaderData?.person.name ?? "Person"} · Recall` }] }),
  component: PersonPage,
})

type Data = NonNullable<Awaited<ReturnType<typeof getPersonPage>>>
type Fact = Data["facts"][number]

const monthFmt = new Intl.DateTimeFormat("en", { month: "long", year: "numeric" })
const dayFmt = new Intl.DateTimeFormat("en", { month: "short", day: "numeric" })

// Facts newest first, grouped by the month they were written.
function byMonth(facts: Fact[]) {
  const groups: { label: string; facts: Fact[] }[] = []
  for (const f of facts) {
    const label = monthFmt.format(f.createdAt)
    const last = groups.at(-1)
    if (last?.label === label) last.facts.push(f)
    else groups.push({ label, facts: [f] })
  }
  return groups
}

function PersonPage() {
  const data = Route.useLoaderData()
  const container = useContainer()
  if (!data) return <NotFound container={container} />
  return <PersonView data={data} />
}

function PersonView({ data }: { data: Data }) {
  const { person, facts, others } = data
  const history = Route.useSearch().history ?? false
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const [dialog, setDialog] = React.useState<"edit" | "merge" | "delete" | null>(null)
  const container = person.container
  const sub = [person.role, person.organization].filter(Boolean).join(" at ")
  const contacts = [...person.emails.map((v) => ({ kind: "Email", v })), ...person.handles.map((v) => ({ kind: "Handle", v }))]
  const refresh = () => void router.invalidate()

  return (
    <div className="page-in flex flex-col gap-6">
      <Button variant="ghost" size="sm" className="-ms-2 self-start text-muted-foreground" render={<Link to="/people" search={{ c: container }} />}>
        <ArrowLeftIcon data-icon="inline-start" />
        People
      </Button>

      <PageHeader
        title={person.name}
        description={sub ? <span className="text-foreground">{sub}</span> : undefined}
        actions={
          <>
            <Button variant="outline" render={<Link to="/ask" search={{ c: container, q: `What do I know about ${person.name}?` }} />}>
              <MessageCircleQuestionMarkIcon data-icon="inline-start" />
              Ask about them
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="outline" size="icon" aria-label={`Actions for ${person.name}`} />}>
                <MoreHorizontalIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuGroup>
                  <DropdownMenuItem onSelect={() => setDialog("edit")}>
                    <PencilIcon />
                    Edit
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setDialog("merge")}>
                    <GitMergeIcon />
                    Merge a duplicate
                  </DropdownMenuItem>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  <DropdownMenuItem variant="destructive" onSelect={() => setDialog("delete")}>
                    <Trash2Icon />
                    Delete
                  </DropdownMenuItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <section aria-labelledby="timeline-heading" className="flex min-w-0 flex-col gap-3 lg:order-1">
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
            <h2 id="timeline-heading" className="text-sm font-medium">
              {history ? plural(facts.length, "fact") : `${plural(person.factCount, "current fact")}`} mentioning them
            </h2>
            <div className="flex items-center gap-2">
              <Switch
                id="show-history"
                checked={history}
                onCheckedChange={(on: boolean) => void navigate({ search: (prev) => ({ ...prev, history: on || undefined }), replace: true })}
              />
              <Label htmlFor="show-history" className="text-caption font-normal text-muted-foreground">
                Include replaced versions
              </Label>
            </div>
          </div>

          {facts.length === 0 ? (
            <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <HistoryIcon />
                </EmptyMedia>
                <EmptyTitle>No current facts mention them</EmptyTitle>
                <EmptyDescription>Every fact that named them was replaced or deleted. Their older versions are still in the history.</EmptyDescription>
              </EmptyHeader>
              {!history && (
                <EmptyContent className="flex-row justify-center">
                  <Button variant="outline" onClick={() => void navigate({ search: (prev) => ({ ...prev, history: true }), replace: true })}>
                    Show replaced versions
                  </Button>
                </EmptyContent>
              )}
            </Empty>
          ) : (
            <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
              {byMonth(facts).map((g, gi) => (
                <section key={g.label} aria-label={g.label} className={cn(gi > 0 && "border-t")}>
                  <h3 className="bg-muted/40 px-4 py-1.5 text-caption font-medium text-muted-foreground">{g.label}</h3>
                  <ItemGroup className="gap-0">
                    {g.facts.map((f) => (
                      <TimelineItem key={f.id} fact={f} container={container} />
                    ))}
                  </ItemGroup>
                </section>
              ))}
            </div>
          )}
        </section>

        <Card className="lg:order-2" role="region" aria-labelledby="about-heading">
          <CardHeader>
            <CardTitle id="about-heading">About</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="flex flex-col gap-4">
              <div className="flex flex-col gap-1">
                <dt className="text-caption font-medium text-muted-foreground">Summary</dt>
                <dd className="text-sm text-pretty">
                  {person.summary || <span className="text-muted-foreground">No summary yet. Edit to write one.</span>}
                </dd>
              </div>
              {person.aliases.length > 0 && (
                <div className="flex flex-col gap-1.5">
                  <dt className="text-caption font-medium text-muted-foreground">Also called</dt>
                  <dd className="flex flex-wrap gap-1">
                    {person.aliases.map((a) => (
                      <Badge key={a} variant="secondary" className="h-auto max-w-full py-0.5 whitespace-normal [overflow-wrap:anywhere]">
                        {a}
                      </Badge>
                    ))}
                  </dd>
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <dt className="text-caption font-medium text-muted-foreground">Contact</dt>
                {contacts.length === 0 ? (
                  <dd className="text-sm text-muted-foreground">None mentioned in the sources.</dd>
                ) : (
                  contacts.map((c) => (
                    <dd key={`${c.kind}-${c.v}`} className="flex min-w-0 items-center justify-between gap-2">
                      <span className="min-w-0 font-mono text-caption [overflow-wrap:anywhere]">
                        <span className="sr-only">{c.kind}: </span>
                        {c.v}
                      </span>
                      <CopyButton text={c.v} label="Copy" variant="ghost" />
                    </dd>
                  ))
                )}
              </div>
              <div className="flex flex-col gap-1">
                <dt className="text-caption font-medium text-muted-foreground">Last mentioned</dt>
                <dd className="text-sm tabular-nums">
                  {person.lastMentionedAt ? (
                    <time dateTime={new Date(person.lastMentionedAt).toISOString()}>{fullDate(person.lastMentionedAt)}</time>
                  ) : (
                    <span className="text-muted-foreground">Never</span>
                  )}
                </dd>
              </div>
              {person.manual && (
                <div className="flex flex-col gap-1">
                  <dt className="text-caption font-medium text-muted-foreground">Edited by hand</dt>
                  <dd className="text-caption text-pretty text-muted-foreground">Extraction keeps the name, role, organization and summary you set.</dd>
                </div>
              )}
            </dl>
          </CardContent>
        </Card>
      </div>

      <EditPersonDialog
        person={person}
        open={dialog === "edit"}
        onOpenChange={(o) => setDialog(o ? "edit" : null)}
        onSaved={() => {
          toast.add({ title: "Saved", type: "success" })
          refresh()
        }}
      />
      <MergePersonDialog
        person={person}
        others={others}
        open={dialog === "merge"}
        onOpenChange={(o) => setDialog(o ? "merge" : null)}
        onMerged={(dropped) => {
          toast.add({ title: `Merged ${dropped.name} into ${person.name}`, type: "success" })
          refresh()
        }}
      />
      <DeletePersonDialog
        person={person}
        open={dialog === "delete"}
        onOpenChange={(o) => setDialog(o ? "delete" : null)}
        onDeleted={() => {
          toast.add({ title: `Deleted ${person.name}`, description: "The facts that mention them stay.", type: "success" })
          void navigate({ to: "/people", search: { c: container } })
        }}
      />
    </div>
  )
}

function TimelineItem({ fact: f, container }: { fact: Fact; container: string }) {
  const current = f.validTo === null
  return (
    <Item role="listitem" className="items-start gap-4 rounded-none border-0 border-t border-t-border px-4 py-3 first:border-t-0 max-sm:flex-col max-sm:gap-1">
      <time
        dateTime={new Date(f.createdAt).toISOString()}
        title={fullDate(f.createdAt)}
        className="shrink-0 pt-px font-mono text-caption text-muted-foreground tabular-nums sm:w-14"
      >
        {dayFmt.format(f.createdAt)}
      </time>
      <ItemContent className="min-w-0 gap-1.5">
        <p className={cn("text-sm text-pretty break-words", !current && "text-muted-foreground line-through decoration-muted-foreground/50")}>{f.fact}</p>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
          {!current && <Badge variant="outline">Replaced</Badge>}
          {f.reviewNote && current && (
            <Badge variant="warning">
              <TriangleAlertIcon />
              Needs review
            </Badge>
          )}
          {f.kind === "static" && <Badge variant="outline">Static</Badge>}
          <span className="inline-flex min-w-0 items-center gap-1">
            {f.source ? <FileTextIcon aria-hidden="true" className="size-3.5 shrink-0" /> : null}
            <span className="max-w-[40ch] truncate" title={f.source?.title ?? f.source?.url ?? undefined}>
              {f.source ? (f.source.title ?? f.source.url ?? "Untitled document") : "Added by hand"}
            </span>
          </span>
          <Link
            to="/"
            search={{ c: container, m: f.id }}
            className="-my-2 inline-flex items-center gap-1 py-2 underline-offset-4 hover:text-foreground hover:underline"
          >
            <HistoryIcon aria-hidden="true" className="size-3.5" />
            History
          </Link>
        </div>
      </ItemContent>
    </Item>
  )
}

function NotFound({ container }: { container: string }) {
  return (
    <Empty className="page-in rounded-xl bg-card shadow-(--shadow-border)">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <UserRoundXIcon />
        </EmptyMedia>
        <EmptyTitle>This person isn't here anymore</EmptyTitle>
        <EmptyDescription>They were merged into someone else or deleted, or the link belongs to another workspace.</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button render={<Link to="/people" search={{ c: container }} />}>Back to People</Button>
      </EmptyContent>
    </Empty>
  )
}

function PersonSkeleton() {
  return (
    <PagePending label="Loading person…">
      <Skeleton className="h-7 w-20" />
      <PageHeaderSkeleton description="w-72" action />
      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex flex-col gap-3">
          <Skeleton className="h-4 w-24" />
          <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
            <div className="bg-muted/40 px-4 py-1.5">
              <Skeleton className="h-3 w-20" />
            </div>
            <ItemGroup className="gap-0">
              {["w-4/5", "w-3/5", "w-2/3", "w-1/2", "w-3/4"].map((w, i) => (
                <Item key={i} className="items-start gap-4 rounded-none border-0 border-t border-t-border px-4 py-3 first:border-t-0 max-sm:flex-col max-sm:gap-1">
                  <Skeleton className="h-4 w-14 shrink-0" />
                  <ItemContent className="min-w-0 gap-2">
                    <Skeleton className={cn("h-4", w)} />
                    <Skeleton className="h-3 w-24" />
                  </ItemContent>
                </Item>
              ))}
            </ItemGroup>
          </div>
        </div>
        <Card>
          <CardHeader>
            <Skeleton className="h-5 w-16" />
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-3/4" />
          </CardContent>
        </Card>
      </div>
    </PagePending>
  )
}
