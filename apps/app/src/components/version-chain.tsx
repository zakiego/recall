// The history of one fact, oldest first: what it said, when it was true, and
// which document taught it. The point of Recall, so it gets its own view.
import * as React from "react"
import { ChevronsUpDownIcon, FileTextIcon } from "lucide-react"

import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { FactDates } from "@/components/fact-dates"
import { getChain } from "@/lib/fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"

type Chain = Awaited<ReturnType<typeof getChain>>
type ChainRow = Chain["versions"][number]

// A long chain shows the original and the newest few; the rest sit behind a
// button, so a fact rewritten every day doesn't push the list off the screen.
const SHOW_NEWEST = 4

export function VersionChain({ id }: { id: string }) {
  const [chain, setChain] = React.useState<Chain>()
  const [error, setError] = React.useState<string>()
  const [attempt, setAttempt] = React.useState(0)
  const [showAll, setShowAll] = React.useState(false)
  const listId = React.useId()

  React.useEffect(() => {
    let alive = true
    setError(undefined)
    getChain({ data: { id } })
      .then((r) => alive && setChain(r))
      .catch((e) => alive && setError(errorMessage(e)))
    return () => {
      alive = false
    }
  }, [id, attempt])

  if (error)
    return (
      <Alert variant="destructive">
        <AlertTitle>Couldn't load the history</AlertTitle>
        <AlertDescription>{error}</AlertDescription>
        <AlertAction>
          <Button size="sm" variant="outline" onClick={() => setAttempt((n) => n + 1)}>
            Retry
          </Button>
        </AlertAction>
      </Alert>
    )
  if (!chain)
    return (
      <div className="flex flex-col gap-3" aria-busy="true" aria-label="Loading history">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-1/2" />
      </div>
    )

  const rows = chain.versions.map((m, i) => ({
    m,
    version: chain.firstVersion + i,
    // A closed last row is a delete, unless the walk stopped before newer versions.
    deleted: m.validTo !== null && i === chain.versions.length - 1 && !chain.hasNewer,
    last: i === chain.versions.length - 1,
  }))
  // Older versions the server didn't send at all (past its walk limit).
  const unsent = chain.firstVersion - 1
  const long = rows.length > SHOW_NEWEST + 2
  const middle = long ? rows.slice(1, -SHOW_NEWEST) : []
  const row = (r: (typeof rows)[number]) => <Version key={r.m.id} m={r.m} version={r.version} last={r.last} deleted={r.deleted} />

  return (
    <div className="flex flex-col gap-2">
      {unsent > 0 && (
        <p className="text-caption text-muted-foreground">
          {plural(unsent, "older version")} {unsent === 1 ? "isn't" : "aren't"} shown. The history starts at v{chain.firstVersion}.
        </p>
      )}
      <ol id={listId} className="flex flex-col" aria-label="Version history, oldest first">
        {long ? (
          <>
            {row(rows[0]!)}
            {/* Not a version, so it isn't a list item to assistive tech. The
                button stays mounted either way, so focus stays on it. */}
            <li key="toggle" role="none" className="relative flex gap-3 pb-4">
              <span aria-hidden="true" className="absolute start-[0.3125rem] top-0 bottom-0 w-px border-s border-dashed border-border" />
              <span aria-hidden="true" className="size-2.5 shrink-0" />
              <Button variant="link" size="caption" aria-expanded={showAll} aria-controls={listId} onClick={() => setShowAll((v) => !v)}>
                <ChevronsUpDownIcon data-icon="inline-start" />
                {showAll ? `Hide ${plural(middle.length, "version")}` : `Show ${plural(middle.length, "more version")}`}
              </Button>
            </li>
            {showAll && middle.map(row)}
            {rows.slice(-SHOW_NEWEST).map(row)}
          </>
        ) : (
          rows.map(row)
        )}
      </ol>
      {chain.hasNewer && <p className="text-caption text-muted-foreground">Newer versions aren't shown.</p>}
    </div>
  )
}

function Version({ m, version, last, deleted }: { m: ChainRow; version: number; last: boolean; deleted: boolean }) {
  const current = m.validTo === null
  return (
    <li className="relative flex gap-3 pb-4 last:pb-0">
      {!last && <span aria-hidden="true" className="absolute start-[0.3125rem] top-4 bottom-0 w-px bg-border" />}
      <span
        aria-hidden="true"
        className={cn(
          "relative mt-1.5 size-2.5 shrink-0 rounded-full ring-4 ring-card",
          current ? "bg-success" : deleted ? "bg-destructive" : "bg-border-strong",
        )}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-caption font-medium text-muted-foreground tabular-nums">v{version}</span>
          {current && <Badge variant="success">Current</Badge>}
          {deleted && <Badge variant="destructive">Deleted</Badge>}
          {m.kind === "static" && <Badge variant="outline">Static</Badge>}
          <FactDates memory={m} current={current} />
        </div>
        <p className={cn("text-sm text-pretty wrap-anywhere", !current && "text-muted-foreground")}>{m.fact}</p>
        <p className="flex flex-wrap items-center gap-x-1.5 text-caption text-muted-foreground">
          <time dateTime={new Date(m.createdAt).toISOString()} title={fullDate(m.createdAt)}>
            {ago(m.createdAt)}
          </time>
          {m.validTo !== null && (
            <>
              <span aria-hidden="true">→</span>
              <time dateTime={new Date(m.validTo).toISOString()} title={fullDate(m.validTo)}>
                {ago(m.validTo)}
              </time>
            </>
          )}
          {m.sourceTitle && (
            <>
              <span aria-hidden="true">·</span>
              <FileTextIcon aria-hidden="true" className="size-3" />
              <span className="max-w-[40ch] truncate" title={m.sourceTitle}>
                {m.sourceTitle}
              </span>
            </>
          )}
        </p>
      </div>
    </li>
  )
}
