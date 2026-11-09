import { ArrowDownIcon, ArrowRightIcon } from "lucide-react"
import { Fragment } from "react"

type Step = { name: string; what: string; detail: string }

// Every value here is the code's own: chunk.ts (800 words, 100 overlap),
// embed.ts (384 dims), pipeline.ts (20 related facts per extraction), search.ts
// (RRF k = 60), ask.ts (12 facts, 6 excerpts).
const WRITE: Step[] = [
  {
    name: "ingest",
    what: "Text, a chat, a URL or a file (PDF, Markdown, HTML, up to 20 MB).",
    detail: "Pages go through Readability, PDFs through unpdf. API keys, tokens and card numbers are masked before anything is stored.",
  },
  {
    name: "chunk + embed",
    what: "About 800 words per chunk, 100 words of overlap.",
    detail: "multilingual-e5-small runs in-process: 384 dimensions, no API call.",
  },
  {
    name: "extract",
    what: "One LLM call per window, through my own AI gateway.",
    detail: "The model sees the new text and the 20 closest current facts in that container.",
  },
  {
    name: "write",
    what: "Add new facts, version changed ones, close stale ones.",
    detail: "A conflict that isn't clear keeps both and flags them for review. IDs and card numbers never become facts.",
  },
]

const READ: Step[] = [
  {
    name: "search",
    what: "SQLite FTS5 and sqlite-vec, merged with reciprocal rank fusion.",
    detail: "No model in the loop, so it answers in milliseconds once the embedder is warm.",
  },
  {
    name: "profile",
    what: "A short static and dynamic summary per container.",
    detail: "Rebuilt in the background after facts change, read without waiting.",
  },
  {
    name: "ask",
    what: "A question answered from the top 12 facts, with citations.",
    detail: "One model call. Says so when memory doesn't cover the question.",
  },
]

function Lane({ title, note, steps, sequence }: { title: string; note: string; steps: Step[]; sequence: boolean }) {
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h3 className="text-heading">{title}</h3>
        <p className="text-sm text-muted-foreground">{note}</p>
      </div>
      <ol className={sequence ? "flex flex-col items-stretch gap-3 lg:flex-row lg:gap-2" : "grid gap-3 md:grid-cols-3"}>
        {steps.map((step, i) => (
          <Fragment key={step.name}>
            {sequence && i > 0 && (
              <li aria-hidden="true" className="flex justify-center text-muted-foreground lg:mt-4.5">
                <ArrowDownIcon className="size-4 lg:hidden" />
                <ArrowRightIcon className="hidden size-4 lg:block" />
              </li>
            )}
            <li className="flex flex-1 flex-col gap-2 rounded-xl bg-card p-4 shadow-[var(--shadow-border)]">
              <span className="font-mono text-sm font-medium">{step.name}</span>
              <p className="text-sm">{step.what}</p>
              <p className="text-sm text-muted-foreground">{step.detail}</p>
            </li>
          </Fragment>
        ))}
      </ol>
    </div>
  )
}

export function Pipeline() {
  return (
    <div className="flex flex-col gap-10">
      <Lane
        title="Write path"
        note="POST /v1/memories returns right away. A job loop does the rest, one job at a time."
        steps={WRITE}
        sequence
      />
      <Lane
        title="Read path"
        note="Three ways to read, side by side. All of them read the same SQLite file."
        steps={READ}
        sequence={false}
      />
    </div>
  )
}
