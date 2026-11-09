import * as React from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { ArrowUpRightIcon, CornerDownLeftIcon, FileTextIcon, PlusIcon, RotateCwIcon, XIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { PersonLinks } from "@/components/person-links"
import { PageHeader } from "@/components/page-header"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge, badgeVariants } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from "@/components/ui/input-group"
import { Kbd } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { useContainer } from "@/hooks/use-container"
import { askMemory } from "@/lib/fns"
import { errorMessage, plural } from "@/lib/format"
import { ASK_TIMEOUT_MS } from "@/lib/limits"
import { cn } from "@/lib/utils"
import { z } from "zod"

export const Route = createFileRoute("/_app/ask")({
  // The question lives in the URL so Back from a cited fact returns to it.
  validateSearch: z.object({ q: z.string().max(2000).optional(), chunks: z.boolean().optional() }),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Ask · Recall" }] }),
  component: AskPage,
})

type Answer = Awaited<ReturnType<typeof askMemory>>
type Citation = Answer["citations"][number]

type State =
  | { status: "idle" }
  | { status: "asking"; question: string; startedAt: number }
  | { status: "done"; result: Answer }
  | { status: "error"; question: string; message: string }

const EXAMPLES = ["What shoes do they wear?", "Di mana dia tinggal sekarang?", "What are they working on right now?", "Ada alergi makanan?"]

// After this long, say why an answer can take a while: asks share one
// gateway slot with extraction, and a long document goes first.
const SLOW_MS = 8_000

// Answers from this session, so following a citation and pressing Back shows
// the same answer instead of paying for another model call.
const answers = new Map<string, Answer>()
const cacheKey = (container: string, q: string, chunks: boolean) => JSON.stringify([container, q, chunks])

function AskPage() {
  const container = useContainer()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const chunks = search.chunks ?? false
  const [question, setQuestion] = React.useState(search.q ?? "")
  const [state, setState] = React.useState<State>(() => {
    const hit = search.q ? answers.get(cacheKey(container, search.q, chunks)) : undefined
    return hit ? { status: "done", result: hit } : { status: "idle" }
  })
  const seq = React.useRef(0)
  const inflight = React.useRef<AbortController | null>(null)
  const inputRef = React.useRef<HTMLTextAreaElement>(null)

  // Stops the ask in flight: the server drops it from the queue or aborts its
  // gateway call, so the next ask doesn't wait behind it.
  const stop = React.useCallback(() => {
    seq.current++
    inflight.current?.abort()
    inflight.current = null
  }, [])
  React.useEffect(() => stop, [stop])

  // Focus the box on desktop only: on a phone it would open the keyboard
  // over the example questions.
  React.useEffect(() => {
    if (!search.q && window.matchMedia("(pointer: fine)").matches) inputRef.current?.focus()
    // Mount only.
  }, [])

  // A new container makes the old answer about someone else.
  const lastContainer = React.useRef(container)
  React.useEffect(() => {
    if (lastContainer.current === container) return
    lastContainer.current = container
    stop()
    setState({ status: "idle" })
  }, [container, stop])

  // A new question while one is waiting replaces it instead of being ignored.
  const run = (q: string) => {
    const term = q.trim()
    if (!term || (state.status === "asking" && state.question === term)) return
    stop()
    const id = seq.current
    const controller = new AbortController()
    inflight.current = controller
    void navigate({ search: (prev) => ({ ...prev, q: term }), replace: true })
    setState({ status: "asking", question: term, startedAt: Date.now() })
    askMemory({ data: { container, question: term, chunks }, signal: controller.signal })
      .then((result) => {
        answers.set(cacheKey(container, term, chunks), result)
        if (id === seq.current) setState({ status: "done", result })
      })
      .catch((e) => id === seq.current && setState({ status: "error", question: term, message: errorMessage(e) }))
      .finally(() => {
        if (inflight.current === controller) inflight.current = null
      })
  }

  const cancel = () => {
    stop()
    setState({ status: "idle" })
    // Back to the box on desktop; on a phone that would open the keyboard.
    if (window.matchMedia("(pointer: fine)").matches) inputRef.current?.focus()
  }

  const asking = state.status === "asking"

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Ask"
        description="A short answer written only from this container's facts, with every claim cited. English or Indonesian. One model call, usually a few seconds."
      />

      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          run(question)
        }}
      >
        <Field>
          <FieldLabel htmlFor="question" className="sr-only">
            Question
          </FieldLabel>
          <InputGroup className="bg-card">
            <InputGroupTextarea
              id="question"
              ref={inputRef}
              rows={2}
              maxLength={2000}
              value={question}
              placeholder="Ask a question about this container…"
              className="field-sizing-content max-h-48 min-h-16 text-base md:text-sm"
              onChange={(e) => setQuestion(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  run(question)
                }
              }}
            />
            <InputGroupAddon align="block-end" className="justify-between">
              <span className="text-caption font-normal max-sm:hidden">
                <Kbd>Shift</Kbd> <Kbd>Enter</Kbd> for a new line
              </span>
              {/* aria-disabled, not disabled: a disabled child greys out the whole InputGroup. */}
              <InputGroupButton
                type="submit"
                variant="default"
                size="sm"
                aria-disabled={!question.trim() || (asking && state.question === question.trim())}
                className="ms-auto aria-disabled:pointer-events-none aria-disabled:opacity-50"
              >
                {asking ? <Spinner data-icon="inline-start" /> : <CornerDownLeftIcon data-icon="inline-start" />}
                Ask
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
        </Field>
        <Field orientation="horizontal" className="w-auto">
          <Switch
            id="ask-chunks"
            checked={chunks}
            onCheckedChange={(v: boolean) => void navigate({ search: (prev) => ({ ...prev, chunks: v || undefined }), replace: true })}
          />
          <FieldLabel htmlFor="ask-chunks" className="font-normal">
            Also read document text
          </FieldLabel>
        </Field>
      </form>

      {state.status === "idle" && (
        <section aria-labelledby="ask-examples" className="flex flex-col gap-2">
          <h2 id="ask-examples" className="text-caption font-medium text-muted-foreground">
            Try
          </h2>
          <div className="flex flex-wrap gap-2">
            {EXAMPLES.map((ex) => (
              <Button
                key={ex}
                variant="outline"
                size="sm"
                onClick={() => {
                  setQuestion(ex)
                  run(ex)
                }}
              >
                {ex}
              </Button>
            ))}
          </div>
        </section>
      )}

      {state.status === "asking" && <Waiting key={state.startedAt} question={state.question} startedAt={state.startedAt} onCancel={cancel} />}

      {state.status === "error" && (
        <Alert variant="destructive" className="enter-in">
          <AlertTitle>Couldn't get an answer</AlertTitle>
          <AlertDescription>{state.message}</AlertDescription>
          <AlertAction>
            <Button size="sm" variant="outline" onClick={() => run(state.question)}>
              <RotateCwIcon data-icon="inline-start" />
              Retry
            </Button>
          </AlertAction>
        </Alert>
      )}

      {state.status === "done" && <AnswerView result={state.result} container={container} />}

      {/* Always mounted, so screen readers hear the answer arrive. */}
      <p role="status" className="sr-only">
        {state.status === "done"
          ? `Answer ready${state.result.citations.length ? `, ${plural(state.result.citations.length, "source")}` : ""}.`
          : state.status === "error"
            ? "Couldn't get an answer."
            : ""}
      </p>
    </div>
  )
}

function Waiting({ question, startedAt, onCancel }: { question: string; startedAt: number; onCancel: () => void }) {
  const [now, setNow] = React.useState(startedAt)
  React.useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  const elapsed = now - startedAt
  return (
    <section aria-busy="true" aria-labelledby="ask-pending" className="enter-in flex flex-col gap-4 rounded-xl bg-card p-4 shadow-(--shadow-border) sm:p-5">
      <h2 id="ask-pending" title={question} className="line-clamp-3 text-sm font-medium text-pretty break-words text-muted-foreground">
        {question}
      </h2>
      <div className="flex flex-col gap-2" aria-hidden="true">
        <Skeleton className="h-4 w-full max-w-[60ch]" />
        <Skeleton className="h-4 w-5/6 max-w-[52ch]" />
        <Skeleton className="h-4 w-2/5" />
      </div>
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <p role="status" className="flex min-w-0 flex-1 items-start gap-2 text-caption text-muted-foreground">
          <Spinner className="mt-0.5 shrink-0" />
          <span>
            Reading the facts and writing an answer…{" "}
            <span aria-hidden="true" className="tabular-nums">
              {Math.floor(elapsed / 1000)} s
            </span>
            {elapsed >= SLOW_MS && (
              <span className="enter-in block text-pretty">
                Recall makes one model call at a time, so a document being extracted goes first. It gives up after{" "}
                {Math.round(ASK_TIMEOUT_MS / 1000)} s, or you can cancel and ask something else.
              </span>
            )}
          </span>
        </p>
        <Button variant="outline" size="sm" onClick={onCancel}>
          <XIcon data-icon="inline-start" />
          Cancel
        </Button>
      </div>
    </section>
  )
}

function AnswerView({ result, container }: { result: Answer; container: string }) {
  const copyText = [
    result.answer,
    ...(result.citations.length ? ["", ...result.citations.map((c) => `[${c.ref}] ${c.text}`)] : []),
  ].join("\n")

  return (
    <section aria-labelledby="ask-question" className="enter-in flex flex-col rounded-xl bg-card shadow-(--shadow-border)">
      <div className="flex flex-col gap-3 p-4 sm:p-5">
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
          <h2 id="ask-question" title={result.question} className="line-clamp-3 min-w-0 flex-1 text-sm font-medium text-pretty break-words text-muted-foreground">
            {result.question}
          </h2>
          {!result.covered ? (
            <Badge variant="warning">Not in memory</Badge>
          ) : (
            result.citations.length === 0 && <Badge variant="warning">No citations</Badge>
          )}
        </div>
        <AnswerText text={result.answer} citations={result.citations} container={container} />
        {result.people.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-caption text-muted-foreground">Read from the person page of</span>
            <PersonLinks people={result.people} container={container} />
          </div>
        )}
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 pt-1">
          <p className="text-caption text-muted-foreground tabular-nums">
            {result.citations.length ? plural(result.citations.length, "source") : "No sources"}
            {result.model && (
              <>
                {" · "}
                <span className="font-mono break-all">{result.model}</span>
              </>
            )}
            {" · "}
            <span className="font-mono">{(result.timingMs / 1000).toFixed(1)} s</span>
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {!result.covered && (
              <Button variant="outline" size="sm" render={<Link to="/add" search={{ c: container }} />}>
                <PlusIcon data-icon="inline-start" />
                {result.model === null ? "Add the first fact" : "Add the missing fact"}
              </Button>
            )}
            <CopyButton text={copyText} label="Copy answer" />
          </div>
        </div>
      </div>

      {result.citations.length > 0 && (
        <>
          <Separator />
          <div className="flex flex-col gap-2 px-4 pt-3 pb-4 sm:px-5">
            <h3 className="text-caption font-medium text-muted-foreground">Sources</h3>
            <ol className="flex flex-col gap-3">
              {result.citations.map((c) => (
                <SourceRow key={c.ref} c={c} container={container} />
              ))}
            </ol>
          </div>
        </>
      )}
    </section>
  )
}

const MARKER = /(\[\d+\])/

// The answer's [n] markers become citation chips; everything else is text.
function AnswerText({ text, citations, container }: { text: string; citations: Citation[]; container: string }) {
  const byRef = new Map(citations.map((c) => [c.ref, c]))
  return (
    <div className="flex max-w-[68ch] flex-col gap-3 text-base/7 text-pretty break-words">
      {/* The chip sits right after its word, like a footnote mark. */}
      {text.replace(/[ \t]+(?=\[\d+\])/g, "").split(/\n{2,}/).map((para, i) => (
        <p key={i} className="whitespace-pre-line">
          {para.split(MARKER).map((part, j) => {
            const ref = part.match(/^\[(\d+)\]$/)?.[1]
            const c = ref ? byRef.get(Number(ref)) : undefined
            return c ? <CitationChip key={j} c={c} container={container} /> : <React.Fragment key={j}>{part}</React.Fragment>
          })}
        </p>
      ))}
    </div>
  )
}

function sourceLink(c: Citation, container: string) {
  return c.type === "memory" ? <Link to="/" search={{ c: container, m: c.id }} /> : <Link to="/add" search={{ c: container, d: c.documentId }} />
}

function CitationChip({ c, container }: { c: Citation; container: string }) {
  return (
    <HoverCard>
      <HoverCardTrigger
        delay={150}
        closeDelay={100}
        render={sourceLink(c, container)}
        aria-label={`Source ${c.ref}: ${c.text.slice(0, 120)}`}
        className={cn(
          badgeVariants({ variant: "secondary" }),
          // A bigger hit area than the 20px pill, without moving the text.
          "relative ms-1 h-[1.15rem] touch-manipulation select-none min-w-[1.15rem] -translate-y-px px-1 align-baseline font-mono text-[0.6875rem] tabular-nums transition-[scale,background-color,color] duration-150 ease-emphasized after:absolute after:-inset-x-0.5 after:-inset-y-2 hover:bg-secondary/70 active:scale-[0.96] data-[popup-open]:bg-primary data-[popup-open]:text-primary-foreground",
        )}
      >
        {c.ref}
      </HoverCardTrigger>
      <HoverCardContent side="top" className="flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2 p-3">
        <SourceMeta c={c} />
        <p className={cn("text-sm text-pretty break-words", c.type === "chunk" && "line-clamp-6")}>{c.text}</p>
        <p className="text-caption text-muted-foreground">{c.type === "memory" ? "Opens it in Memories" : "Opens the document on Add"}</p>
      </HoverCardContent>
    </HoverCard>
  )
}

function SourceMeta({ c }: { c: Citation }) {
  return c.type === "memory" ? (
    <Badge variant="soft" className="self-start">
      Fact {c.ref}
    </Badge>
  ) : (
    <span className="flex min-w-0 items-center gap-2">
      <Badge variant="secondary" className="shrink-0">
        <FileTextIcon aria-hidden="true" />
        Document {c.ref}
      </Badge>
      <span className="truncate text-caption text-muted-foreground" title={c.documentTitle ?? undefined}>
        {c.documentTitle ?? "Untitled"}
      </span>
    </span>
  )
}

function SourceRow({ c, container }: { c: Citation; container: string }) {
  return (
    <li className="flex gap-3">
      <span className="w-5 shrink-0 pt-px text-end font-mono text-caption text-muted-foreground tabular-nums">{c.ref}</span>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className={cn("text-sm text-pretty break-words", c.type === "chunk" && "line-clamp-3 text-muted-foreground")}>{c.text}</p>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
          {c.type === "memory" ? (
            <span>Fact</span>
          ) : (
            <span className="flex min-w-0 items-center gap-1">
              <FileTextIcon aria-hidden="true" className="size-3.5 shrink-0" />
              <span className="max-w-[40ch] truncate" title={c.documentTitle ?? undefined}>
                {c.documentTitle ?? "Untitled document"}
              </span>
            </span>
          )}
          <Button variant="link" size="caption" render={sourceLink(c, container)}>
            {c.type === "memory" ? "Open in Memories" : "Open the document"}
            <ArrowUpRightIcon data-icon="inline-end" />
          </Button>
        </div>
      </div>
    </li>
  )
}
