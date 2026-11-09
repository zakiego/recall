import * as React from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { deleteMemory, updateMemory } from "@/lib/fns"
import { errorMessage } from "@/lib/format"

export type EditableMemory = { id: string; fact: string; keywords: string; kind: "static" | "dynamic" }

export function EditMemoryDialog({
  memory,
  onOpenChange,
  onSaved,
}: {
  memory: EditableMemory | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [fact, setFact] = React.useState("")
  const [keywords, setKeywords] = React.useState("")
  const [kind, setKind] = React.useState<"static" | "dynamic">("dynamic")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => {
    if (!memory) return
    setFact(memory.fact)
    setKeywords(memory.keywords)
    setKind(memory.kind)
    setError(undefined)
  }, [memory])

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!memory) return
    if (!fact.trim()) {
      setError("A fact can't be empty. To remove it, delete it instead.")
      return
    }
    setBusy(true)
    try {
      await updateMemory({ data: { id: memory.id, fact: fact.trim(), keywords: keywords.trim(), kind } })
      onSaved()
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={memory !== null} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>Edit fact</DialogTitle>
            <DialogDescription>Saving adds a new version. The old one stays in the history.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={error !== undefined}>
              <FieldLabel htmlFor="edit-fact">Fact</FieldLabel>
              <Textarea
                id="edit-fact"
                value={fact}
                maxLength={500}
                rows={3}
                aria-invalid={error !== undefined}
                onChange={(e) => setFact(e.target.value)}
              />
              {error ? <FieldError>{error}</FieldError> : <FieldDescription>One statement that's true now.</FieldDescription>}
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-keywords">Keywords</FieldLabel>
              <Input id="edit-keywords" value={keywords} maxLength={500} onChange={(e) => setKeywords(e.target.value)} />
              <FieldDescription>Search terms in English and Indonesian, separated by spaces.</FieldDescription>
            </Field>
            <Field>
              <FieldLabel id="edit-kind-label">Kind</FieldLabel>
              <ToggleGroup
                aria-labelledby="edit-kind-label"
                variant="outline"
                value={[kind]}
                onValueChange={(v: string[]) => v[0] && setKind(v[0] as "static" | "dynamic")}
              >
                <ToggleGroupItem value="dynamic">Dynamic</ToggleGroupItem>
                <ToggleGroupItem value="static">Static</ToggleGroupItem>
              </ToggleGroup>
              <FieldDescription>Static for lasting traits, dynamic for things that change.</FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner data-icon="inline-start" />}
              Save version
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// `keep` is set when this delete settles a conflict: the fact named in `keep`
// is the one being kept, so the copy talks about "the other fact".
export function DeleteMemoryDialog({
  memory,
  onOpenChange,
  onDeleted,
}: {
  memory: { id: string; fact: string; keep?: string } | null
  onOpenChange: (open: boolean) => void
  onDeleted: () => void
}) {
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  // Keep the fact on screen while the dialog animates out.
  const last = React.useRef(memory)
  if (memory) last.current = memory

  const confirm = async () => {
    if (!memory) return
    setBusy(true)
    setError(undefined)
    try {
      await deleteMemory({ data: { id: memory.id } })
      onDeleted()
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={memory !== null} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{last.current?.keep ? "Delete the other fact?" : "Delete this fact?"}</AlertDialogTitle>
          <AlertDialogDescription className="wrap-anywhere">
            “{last.current?.fact}” stops showing up in search and the profile. It stays in the history, marked deleted.
            {last.current?.keep && <> “{last.current.keep}” stays current, and both review flags clear.</>}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertTitle>Couldn't delete it</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>{last.current?.keep ? "Cancel" : "Keep it"}</AlertDialogCancel>
          <AlertDialogAction variant="danger" disabled={busy} onClick={() => void confirm()}>
            {busy && <Spinner data-icon="inline-start" />}
            {last.current?.keep ? "Delete the other fact" : "Delete fact"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
