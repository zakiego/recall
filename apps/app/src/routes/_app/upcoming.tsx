import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import {
  BellIcon,
  BellOffIcon,
  CalendarClockIcon,
  CalendarDaysIcon,
  CalendarRangeIcon,
  CalendarSearchIcon,
  ChevronRightIcon,
  MailWarningIcon,
  PencilIcon,
} from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { DUE_SOON_DAYS } from "@/components/fact-dates"
import { EditMemoryDialog, editableDates, type EditableMemory } from "@/components/memory-dialogs"
import { PageHeader } from "@/components/page-header"
import { VersionChain } from "@/components/version-chain"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldContent, FieldDescription, FieldLabel, FieldTitle } from "@/components/ui/field"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { PagePending, PageHeaderSkeleton, TableSkeleton } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { Toggle } from "@/components/ui/toggle"
import { useContainer } from "@/hooks/use-container"
import { dated, formatDate, relativeDays } from "@/lib/dates"
import { getUpcoming, UPCOMING_WINDOWS } from "@/lib/fns"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"

type Window = (typeof UPCOMING_WINDOWS)[number]

export const Route = createFileRoute("/_app/upcoming")({
  validateSearch: z.object({
    days: z.union([z.literal(7), z.literal(30), z.literal(90), z.literal(365)]).optional(),
    all: z.boolean().optional(),
  }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c, days: search.days ?? 30, all: search.all ?? false }),
  loader: ({ deps, context }) => getUpcoming({ data: { container: deps.c ?? context.defaultContainer, days: deps.days, all: deps.all } }),
  pendingComponent: UpcomingSkeleton,
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Upcoming · Recall" }] }),
  component: UpcomingPage,
})

type Row = Awaited<ReturnType<typeof getUpcoming>>["memories"][number]

const WINDOW_COPY: Record<Window, { title: string; Icon: typeof CalendarDaysIcon }> = {
  7: { title: "Next 7 days", Icon: CalendarClockIcon },
  30: { title: "Next 30 days", Icon: CalendarDaysIcon },
  90: { title: "Next 3 months", Icon: CalendarRangeIcon },
  365: { title: "Next year", Icon: CalendarSearchIcon },
}

// Groups by how soon, in the order they're read.
const GROUPS = [
  { key: "past", label: "Past due", test: (d: number) => d < 0 },
  { key: "week", label: "Within a week", test: (d: number) => d >= 0 && d <= 7 },
  { key: "month", label: "Within a month", test: (d: number) => d > 7 && d <= 31 },
  { key: "later", label: "Later", test: (d: number) => d > 31 },
] as const

function leadText(leads: number[]) {
  const parts = leads.map((d) => (d === 0 ? "on the day" : `${d} ${d === 1 ? "day" : "days"} before`))
  const text = parts.length <= 1 ? (parts[0] ?? "") : `${parts.slice(0, -1).join(", ")} and ${parts.at(-1)}`
  return text.charAt(0).toUpperCase() + text.slice(1)
}

function UpcomingPage() {
  const container = useContainer()
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const days: Window = search.days ?? 30
  const all = search.all ?? false
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  const [open, setOpen] = React.useState<string | null>(null)
  const [editing, setEditing] = React.useState<EditableMemory | null>(null)

  React.useEffect(() => setOpen(null), [data])

  const setDays = (d: Window) => void navigate({ search: (prev) => ({ ...prev, days: d === 30 ? undefined : d }), replace: true })
  const setAll = (on: boolean) => void navigate({ search: (prev) => ({ ...prev, all: on || undefined }), replace: true })

  const groups = GROUPS.map((g) => ({ ...g, rows: data.memories.filter((m) => g.test(m.daysLeft)) })).filter((g) => g.rows.length > 0)
  const ahead = data.memories.filter((m) => m.daysLeft >= 0).length

  return (
    <div className="page-in flex flex-col gap-6">
      <PageHeader
        title="Upcoming"
        description={
          <>
            Facts {all ? "in every container" : <>in <span className="font-mono wrap-anywhere text-foreground">{container}</span></>} that end, renew or
            fall due soon. Facts that came due in the last {data.pastDays} days stay here as past due.
          </>
        }
        actions={
          <Toggle variant="outline" pressed={all} onPressedChange={setAll}>
            All containers
          </Toggle>
        }
      />

      <ReminderStatus reminders={data.reminders} />

      <RadioGroup
        aria-label="How far ahead"
        value={String(days)}
        onValueChange={(v) => setDays(Number(v) as Window)}
        className="grid grid-cols-2 gap-3 lg:grid-cols-4"
      >
        {UPCOMING_WINDOWS.map((w) => {
          const { title, Icon } = WINDOW_COPY[w]
          const n = data.counts[w]
          return (
            <FieldLabel key={w} htmlFor={`window-${w}`} className="bg-card">
              <Field orientation="horizontal">
                <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                <FieldContent>
                  <FieldTitle>{title}</FieldTitle>
                  <FieldDescription className="tabular-nums">{n === 0 ? "Nothing due" : `${plural(n, "fact")} due`}</FieldDescription>
                </FieldContent>
                <RadioGroupItem value={String(w)} id={`window-${w}`} />
              </Field>
            </FieldLabel>
          )
        })}
      </RadioGroup>

      {groups.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CalendarClockIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing due in the {WINDOW_COPY[days].title.toLowerCase()}</EmptyTitle>
            <EmptyDescription>
              Recall picks up due dates when it reads what you add, like “the contract ends 31 December 2027”. You can also set one by editing a
              fact.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row flex-wrap justify-center">
            {days !== 365 && (
              <Button variant="outline" onClick={() => setDays(365)}>
                Look a year ahead
              </Button>
            )}
            <Button variant={days === 365 ? "default" : "ghost"} render={<Link to="/" search={{ c: container }} />}>
              Go to memories
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <div className="flex flex-col gap-6">
          <p className="sr-only" role="status">
            {plural(ahead, "fact")} due in the {WINDOW_COPY[days].title.toLowerCase()}
          </p>
          {groups.map((g) => (
            <section key={g.key} aria-labelledby={`group-${g.key}`} className="flex flex-col gap-2">
              <h2 id={`group-${g.key}`} className="flex items-baseline gap-2 text-sm font-medium">
                {g.label}
                <span className="text-caption font-normal text-muted-foreground tabular-nums">{g.rows.length}</span>
              </h2>
              <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
                <Table className="table-fixed">
                  <colgroup>
                    <col className="hidden w-36 sm:table-column" />
                    <col />
                    <col className="w-28 pointer-coarse:w-32" />
                  </colgroup>
                  <TableHeader className="sr-only">
                    <TableRow>
                      <TableHead className="hidden sm:table-cell">Due</TableHead>
                      <TableHead>Fact</TableHead>
                      <TableHead>Actions</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {g.rows.map((m) => (
                      <UpcomingRow
                        key={m.id}
                        m={m}
                        showContainer={all}
                        expanded={open === m.id}
                        onToggle={() => setOpen(open === m.id ? null : m.id)}
                        onEdit={() => setEditing({ id: m.id, fact: m.fact, keywords: m.keywords, kind: m.kind, ...editableDates(m) })}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          ))}
          {data.total > data.memories.length && (
            <p className="text-center text-caption text-muted-foreground tabular-nums">
              Showing the soonest {data.memories.length.toLocaleString("en")} of {plural(data.total, "fact")}. The rest are further out.
            </p>
          )}
        </div>
      )}

      <EditMemoryDialog
        memory={editing}
        onOpenChange={(o) => !o && setEditing(null)}
        onSaved={() => {
          toast.add({ title: "Saved as a new version", type: "success" })
          void router.invalidate()
        }}
      />
    </div>
  )
}

function UpcomingRow({ m, showContainer, expanded, onToggle, onEdit }: { m: Row; showContainer: boolean; expanded: boolean; onToggle: () => void; onEdit: () => void }) {
  const due = dated(m.dueDate, m.duePrecision)!
  const soon = m.daysLeft <= DUE_SOON_DAYS
  const event = dated(m.eventDate, m.eventPrecision)
  return (
    <>
      <TableRow data-state={expanded ? "selected" : undefined} className="align-top">
        <TableCell className="hidden py-3 align-top sm:table-cell">
          <time dateTime={m.dueDate!} className="block font-mono text-sm tabular-nums">
            {formatDate(due)}
          </time>
          <span className={cn("text-caption tabular-nums", soon ? "font-medium text-warning-fg" : "text-muted-foreground")}>
            {relativeDays(m.daysLeft)}
          </span>
        </TableCell>
        <TableCell className="py-3 align-top whitespace-normal">
          {/* On a phone the date sits above the fact instead of in its own column. */}
          <p className="mb-0.5 flex flex-wrap items-baseline gap-x-2 sm:hidden">
            <time dateTime={m.dueDate!} className="font-mono text-sm tabular-nums">
              {formatDate(due)}
            </time>
            <span className={cn("text-caption tabular-nums", soon ? "font-medium text-warning-fg" : "text-muted-foreground")}>
              {relativeDays(m.daysLeft)}
            </span>
          </p>
          <p className="text-sm text-pretty break-words">{m.fact}</p>
          {(showContainer || event || m.versions > 1 || m.kind === "static") && (
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              {showContainer && (
                <Badge variant="secondary" className="h-auto max-w-full font-mono whitespace-normal wrap-anywhere">
                  {m.container}
                </Badge>
              )}
              {event && <span className="text-caption text-muted-foreground tabular-nums">Happened {formatDate(event)}</span>}
              {m.kind === "static" && <Badge variant="outline">Static</Badge>}
              {m.versions > 1 && <Badge variant="soft">v{m.versions}</Badge>}
            </div>
          )}
        </TableCell>
        <TableCell className="py-2 align-top text-end">
          <div className="flex justify-end gap-0.5 pointer-coarse:gap-2">
            <Button variant="ghost" size="icon-sm" aria-label={`Edit “${m.fact.slice(0, 40)}”`} onClick={onEdit}>
              <PencilIcon />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-expanded={expanded}
              aria-controls={`upcoming-chain-${m.id}`}
              aria-label={`${expanded ? "Hide" : "Show"} history of “${m.fact.slice(0, 40)}”`}
              onClick={onToggle}
            >
              <ChevronRightIcon className={cn("transition-transform duration-200 ease-emphasized", expanded && "rotate-90")} />
            </Button>
          </div>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={3} id={`upcoming-chain-${m.id}`} className="enter-in px-4 pt-1 pb-4 whitespace-normal">
            <VersionChain id={m.id} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

function ReminderStatus({ reminders }: { reminders: Awaited<ReturnType<typeof getUpcoming>>["reminders"] }) {
  const settings = (
    <AlertAction>
      <Button size="sm" variant="outline" render={<Link to="/settings/reminders" />}>
        {reminders.enabled ? "Edit reminders" : "Set up"}
      </Button>
    </AlertAction>
  )
  if (!reminders.configured) {
    return (
      <Alert>
        <MailWarningIcon />
        <AlertTitle>Email isn't set up on this server</AlertTitle>
        <AlertDescription>
          <p>
            No reminder emails go out until whoever runs Recall sets <code>RESEND_API_KEY</code> and <code>RECALL_EMAIL_FROM</code>.
          </p>
        </AlertDescription>
      </Alert>
    )
  }
  if (!reminders.enabled || !reminders.email) {
    return (
      <Alert>
        <BellOffIcon />
        <AlertTitle>Reminders are off</AlertTitle>
        <AlertDescription>Turn them on to get an email before something here is due.</AlertDescription>
        {settings}
      </Alert>
    )
  }
  return (
    <Alert>
      <BellIcon />
      <AlertTitle>Email reminders are on</AlertTitle>
      <AlertDescription>
        <span>
          {leadText(reminders.leadDays)}, to <span className="font-medium wrap-anywhere text-foreground">{reminders.email}</span>, after 08:00{" "}
          <span className="font-mono">{reminders.timezone}</span>.
        </span>
      </AlertDescription>
      {settings}
    </Alert>
  )
}

function UpcomingSkeleton() {
  return (
    <PagePending label="Loading upcoming facts…">
      <PageHeaderSkeleton action />
      <Skeleton className="h-16 w-full rounded-xl" />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {UPCOMING_WINDOWS.map((w) => (
          <Field key={w} orientation="horizontal" className="rounded-lg border bg-card p-2.5">
            <Skeleton className="size-4 shrink-0" />
            <FieldContent className="gap-2">
              <Skeleton className="h-4 w-24" />
              <Skeleton className="h-3 w-16" />
            </FieldContent>
            <Skeleton className="size-4 shrink-0 rounded-full" />
          </Field>
        ))}
      </div>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-4 w-28" />
        <TableSkeleton
          header={false}
          rows={4}
          columns={[{ className: "hidden w-36 sm:table-cell", short: "w-24" }, { sub: true }, { className: "w-28", short: "w-16", align: "end" }]}
        />
      </div>
    </PagePending>
  )
}
