import * as React from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { ArrowUpRightIcon, FileTextIcon, RotateCwIcon, SearchIcon, SearchXIcon } from "lucide-react"
import { z } from "zod"

import { PageHeader } from "@/components/page-header"
import { PersonLinks } from "@/components/person-links"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldLabel } from "@/components/ui/field"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { searchMemories } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"

export const Route = createFileRoute("/_app/search")({
  validateSearch: z.object({ q: z.string().max(2000).optional(), chunks: z.boolean().optional() }),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Search · Recall" }] }),
  component: SearchPage,
})

type Result = Awaited<ReturnType<typeof searchMemories>>
type Hit = Result["results"][number]

const EXAMPLES = ["What shoes do they wear?", "kerja di mana?", "food allergies", "current projects"]

function SearchPage() {
  const container = useContainer()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const [q, setQ] = React.useState(search.q ?? "")
  const [result, setResult] = React.useState<Result>()
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const chunks = search.chunks ?? false
  const seq = React.useRef(0)
  const [attempt, setAttempt] = React.useState(0)
  const inputRef = React.useRef<HTMLInputElement>(null)

  // Focus the box on desktop only: on a phone it would open the keyboard
  // over the example queries.
  React.useEffect(() => {
    if (window.matchMedia("(pointer: fine)").matches) inputRef.current?.focus()
  }, [])

  // Search is local and fast, so it runs as you type, debounced. The query
  // lives in the URL so a search can be linked and survives a reload.
  React.useEffect(() => {
    const term = q.trim()
    const id = ++seq.current
    if (!term) {
      setResult(undefined)
      setError(undefined)
      setBusy(false)
      return
    }
    setBusy(true)
    const t = setTimeout(() => {
      void navigate({ search: (prev) => ({ ...prev, q: term }), replace: true })
      searchMemories({ data: { container, q: term, chunks } })
        .then((r) => {
          if (id !== seq.current) return
          setResult(r)
          setError(undefined)
        })
        .catch((e) => id === seq.current && setError(errorMessage(e)))
        .finally(() => id === seq.current && setBusy(false))
    }, 250)
    return () => clearTimeout(t)
  }, [q, container, chunks, navigate, attempt])

  const top = result?.results[0]?.score ?? 1

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Search"
        description="The same hybrid search agents get: keywords and meaning, in English or Indonesian, over current facts only."
      />

      <form
        role="search"
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault()
        }}
      >
        <Field className="flex-1">
          <FieldLabel htmlFor="q" className="sr-only">
            Search query
          </FieldLabel>
          <InputGroup className="h-10">
            <InputGroupAddon>{busy ? <Spinner /> : <SearchIcon />}</InputGroupAddon>
            <InputGroupInput
              id="q"
              ref={inputRef}
              placeholder="Ask about this container"
              value={q}
              maxLength={2000}
              autoComplete="off"
              spellCheck={false}
              enterKeyHint="search"
              onChange={(e) => setQ(e.target.value)}
            />
          </InputGroup>
        </Field>
        <Field orientation="horizontal" className="w-auto shrink-0 sm:pb-2">
          <Switch
            id="chunks"
            checked={chunks}
            onCheckedChange={(v: boolean) => void navigate({ search: (prev) => ({ ...prev, chunks: v || undefined }), replace: true })}
          />
          <FieldLabel htmlFor="chunks" className="font-normal">
            Include document text
          </FieldLabel>
        </Field>
      </form>

      {error && (
        <Alert variant="destructive">
          <AlertTitle>Search failed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
          <AlertAction>
            <Button size="sm" variant="outline" onClick={() => setAttempt((n) => n + 1)}>
              <RotateCwIcon data-icon="inline-start" />
              Retry
            </Button>
          </AlertAction>
        </Alert>
      )}

      {!q.trim() && (
        <section aria-labelledby="examples" className="flex flex-col gap-2">
          <h2 id="examples" className="text-caption font-medium text-muted-foreground">
            Try
          </h2>
          <div className="flex flex-wrap gap-2">
            {EXAMPLES.map((ex) => (
              <Button key={ex} variant="outline" size="sm" onClick={() => setQ(ex)}>
                {ex}
              </Button>
            ))}
          </div>
        </section>
      )}

      {q.trim() && !result && !error && (
        <div className="flex flex-col gap-2" aria-busy="true" aria-label="Searching">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-16 w-full rounded-xl" />
          ))}
        </div>
      )}

      {result && q.trim() && (
        <section aria-label="Results" className="flex flex-col gap-3">
          <p role="status" className="text-caption text-muted-foreground tabular-nums">
            {result.results.length === 0 ? "No matches" : `${result.results.length} ${result.results.length === 1 ? "match" : "matches"}`} in{" "}
            <span className="font-mono wrap-anywhere">{result.container}</span> · {result.timingMs} ms
          </p>
          {result.results.length === 0 ? (
            <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <SearchXIcon />
                </EmptyMedia>
                <EmptyTitle className="wrap-anywhere">Nothing matches “{result.q}”</EmptyTitle>
                <EmptyDescription>
                  {chunks ? "Try fewer words, or check the container in the header." : "Search the raw documents too, or check the container in the header."}
                </EmptyDescription>
              </EmptyHeader>
              {!chunks && (
                <EmptyContent>
                  <Button variant="outline" onClick={() => void navigate({ search: (prev) => ({ ...prev, chunks: true }), replace: true })}>
                    Include document text
                  </Button>
                </EmptyContent>
              )}
            </Empty>
          ) : (
            <ol className="flex flex-col overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
              {result.results.map((h, i) => (
                <ResultRow key={`${h.type}-${h.id}`} hit={h} rank={i + 1} relative={h.score / top} container={result.container} />
              ))}
            </ol>
          )}
        </section>
      )}
    </div>
  )
}

function ResultRow({ hit, rank, relative, container }: { hit: Hit; rank: number; relative: number; container: string }) {
  return (
    <li className="flex gap-3 border-b px-4 py-3 last:border-b-0">
      <span className="w-5 shrink-0 pt-0.5 text-end text-caption text-muted-foreground tabular-nums">{rank}</span>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <p className={hit.type === "chunk" ? "line-clamp-4 text-sm text-pretty break-words text-muted-foreground" : "text-sm text-pretty break-words"}>{hit.text}</p>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
          {hit.type === "memory" ? (
            <>
              <Badge variant="soft">Fact</Badge>
              {hit.kind === "static" && <Badge variant="outline">Static</Badge>}
              <time dateTime={new Date(hit.createdAt).toISOString()} title={fullDate(hit.createdAt)}>
                {ago(hit.createdAt)}
              </time>
              <PersonLinks people={hit.people} container={container} />
            </>
          ) : (
            <>
              <Badge variant="secondary">
                <FileTextIcon aria-hidden="true" />
                Document
              </Badge>
              <span className="max-w-[40ch] truncate" title={hit.documentTitle ?? undefined}>
                {hit.documentTitle ?? "Untitled"}
              </span>
              <span className="tabular-nums">part {hit.idx + 1}</span>
            </>
          )}
          <span className="font-mono tabular-nums max-sm:hidden">
            {hit.ftsRank ? `kw #${hit.ftsRank}` : "kw –"} · {hit.similarity !== undefined ? `sim ${hit.similarity.toFixed(2)}` : "sim –"}
          </span>
          {/* Same destinations as Ask's sources: the fact with its history, or the document. */}
          <Button
            variant="link"
            size="caption"
            render={
              hit.type === "memory" ? <Link to="/" search={{ c: container, m: hit.id }} /> : <Link to="/add" search={{ c: container, d: hit.documentId }} />
            }
          >
            {hit.type === "memory" ? "Open in Memories" : "Open the document"}
            <ArrowUpRightIcon data-icon="inline-end" />
          </Button>
        </div>
      </div>
      <div className="flex w-16 shrink-0 flex-col items-end gap-1 pt-0.5 sm:w-24">
        <span className="font-mono text-caption tabular-nums">
          <span className="sr-only">Score </span>
          {hit.score.toFixed(3)}
        </span>
        {/* Data being read: the bar doesn't animate. */}
        <Progress value={Math.max(relative, 0.04) * 100} aria-hidden="true" className="w-full [&_[data-slot=progress-indicator]]:transition-none" />
      </div>
    </li>
  )
}
