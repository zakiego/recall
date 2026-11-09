// The history of one fact, oldest first: what it said, when it was true, and
// which document taught it. The point of Recall, so it gets its own view.
import * as React from "react"
import { FileTextIcon } from "lucide-react"

import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { getChain } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { cn } from "@/lib/utils"

type ChainRow = Awaited<ReturnType<typeof getChain>>[number]

export function VersionChain({ id }: { id: string }) {
  const [rows, setRows] = React.useState<ChainRow[]>()
  const [error, setError] = React.useState<string>()
  const [attempt, setAttempt] = React.useState(0)

  React.useEffect(() => {
    let alive = true
    setError(undefined)
    getChain({ data: { id } })
      .then((r) => alive && setRows(r))
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
  if (!rows)
    return (
      <div className="flex flex-col gap-3" aria-busy="true" aria-label="Loading history">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-1/2" />
      </div>
    )

  return (
    <ol className="flex flex-col" aria-label="Version history, oldest first">
      {rows.map((m, i) => {
        const current = m.validTo === null
        const last = i === rows.length - 1
        const deleted = !current && last
        return (
          <li key={m.id} className="relative flex gap-3 pb-4 last:pb-0">
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
                <span className="text-caption font-medium text-muted-foreground tabular-nums">v{i + 1}</span>
                {current && <Badge variant="success">Current</Badge>}
                {deleted && <Badge variant="destructive">Deleted</Badge>}
                {m.kind === "static" && <Badge variant="outline">Static</Badge>}
              </div>
              <p className={cn("text-sm text-pretty break-words", !current && "text-muted-foreground")}>{m.fact}</p>
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
      })}
    </ol>
  )
}
