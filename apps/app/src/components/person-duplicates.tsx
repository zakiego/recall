import * as React from "react"
import { Link, useRouter } from "@tanstack/react-router"

import { ErrorAlert } from "@/components/error-alert"
import { Button } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { ConfirmMergeDialog } from "@/components/person-dialogs"
import { getMergeSuggestions } from "@/lib/fns"
import { explainError, plural, type ExplainedError } from "@/lib/format"

type Suggestion = Awaited<ReturnType<typeof getMergeSuggestions>>[number]

// Pairs someone marked "Not the same", kept per browser. Nothing else depends on it.
const DISMISSED_KEY = "recall.people.notDuplicates"
const pairKey = (s: Suggestion) => [s.keep.id, s.drop.id].sort().join("|")

function readDismissed(): Set<string> {
  try {
    const v = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? "[]")
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [])
  } catch {
    return new Set()
  }
}

function writeDismissed(keys: Set<string>) {
  try {
    localStorage.setItem(DISMISSED_KEY, JSON.stringify([...keys].slice(-500)))
  } catch {
    // Private windows can refuse storage; the pair just shows up again next visit.
  }
}

const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

// Likely duplicate people in the container, each one click from a merge. The
// list loads after the page (the check runs the embedding model) and stays
// hidden while it loads or when there is nothing to suggest.
export function PossibleDuplicates({ container }: { container: string }) {
  const router = useRouter()
  const [found, setFound] = React.useState<Suggestion[] | null>(null)
  const [error, setError] = React.useState<ExplainedError>()
  const [dismissed, setDismissed] = React.useState<Set<string>>(() => new Set())
  const [merging, setMerging] = React.useState<Suggestion | null>(null)
  const [reload, setReload] = React.useState(0)

  React.useEffect(() => setDismissed(readDismissed()), [])

  React.useEffect(() => {
    let live = true
    setError(undefined)
    getMergeSuggestions({ data: { container } })
      .then((rows) => live && setFound(rows))
      .catch((err) => live && setError(explainError(err, "The duplicate check", "The duplicate check didn't run.")))
    return () => {
      live = false
    }
  }, [container, reload])

  if (error) return <ErrorAlert title="Couldn't check for duplicates" error={error} onRetry={() => setReload((n) => n + 1)} />

  const shown = (found ?? []).filter((s) => !dismissed.has(pairKey(s)))
  if (!shown.length) return null

  const notSame = (s: Suggestion) => {
    const next = new Set(dismissed).add(pairKey(s))
    setDismissed(next)
    writeDismissed(next)
  }

  return (
    <section aria-labelledby="possible-duplicates" className="enter-in overflow-hidden rounded-xl border border-transparent bg-card shadow-(--shadow-border)">
      <div className="border-b px-5 py-3">
        <h2 id="possible-duplicates" className="text-sm font-medium">
          Possible duplicates <span className="font-normal text-muted-foreground tabular-nums">{shown.length}</span>
        </h2>
      </div>
      <ul className="divide-y">
        {shown.map((s) => (
          <li key={pairKey(s)} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3">
            <div className="min-w-0 flex-1 basis-64">
              <p className="text-sm break-words">
                <PersonLink person={s.drop} container={container} /> <span className="text-muted-foreground">into</span>{" "}
                <PersonLink person={s.keep} container={container} />
              </p>
              <p className="mt-0.5 text-caption break-words text-muted-foreground">
                {sentence(s.reasons.join(", "))} · {plural(s.drop.factCount, "fact")} {s.drop.factCount === 1 ? "moves" : "move"} to {s.keep.name}
              </p>
            </div>
            <div className="flex gap-2">
              <Button variant="ghost" size="sm" onClick={() => notSame(s)}>
                Not the same
              </Button>
              <Button variant="outline" size="sm" onClick={() => setMerging(s)}>
                Merge
              </Button>
            </div>
          </li>
        ))}
      </ul>
      {merging && (
        <ConfirmMergeDialog
          keep={merging.keep}
          drop={merging.drop}
          open={!!merging}
          onOpenChange={(o) => !o && setMerging(null)}
          onMerged={() => {
            toast.add({ title: `Merged ${merging.drop.name} into ${merging.keep.name}`, type: "success" })
            setFound((cur) => cur?.filter((x) => x.keep.id !== merging.drop.id && x.drop.id !== merging.drop.id) ?? null)
            setReload((n) => n + 1)
            void router.invalidate()
          }}
        />
      )}
    </section>
  )
}

function PersonLink({ person, container }: { person: { id: string; name: string }; container: string }) {
  return (
    <Link to="/people/$id" params={{ id: person.id }} search={{ c: container }} className="font-medium underline-offset-4 hover:underline">
      {person.name}
    </Link>
  )
}
