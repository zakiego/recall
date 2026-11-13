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
import { DateField } from "@/components/date-field"
import { dated, parseDate, toDateString } from "@/lib/dates"
import { deleteMemory, updateMemory } from "@/lib/fns"
import { errorMessage } from "@/lib/format"

// Dates in short form ("2026-11-05", "2027-12", "2027"), "" for none.
export type EditableMemory = { id: string; fact: string; keywords: string; kind: "static" | "dynamic"; eventDate: string; dueDate: string }

// The short-form dates of a stored row, for EditableMemory.
export function editableDates(m: { eventDate: string | null; eventPrecision: string | null; dueDate: string | null; duePrecision: string | null }) {
  const event = dated(m.eventDate, m.eventPrecision)
  const due = dated(m.dueDate, m.duePrecision)
  return { eventDate: event ? toDateString(event) : "", dueDate: due ? toDateString(due) : "" }
}

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
  const [eventDate, setEventDate] = React.useState("")
  const [dueDate, setDueDate] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const [dateErrors, setDateErrors] = React.useState<{ event?: string; due?: string }>({})
  // Remounts the date fields once the new fact's dates are in state, so their precision starts from them.
  const [formKey, setFormKey] = React.useState(0)

  React.useEffect(() => {
    if (!memory) return
    setFact(memory.fact)
    setKeywords(memory.keywords)
    setKind(memory.kind)
    setEventDate(memory.eventDate)
    setDueDate(memory.dueDate)
    setError(undefined)
    setDateErrors({})
    setFormKey((k) => k + 1)
  }, [memory])

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!memory) return
    if (!fact.trim()) {
      setError("A fact can't be empty. To remove it, delete it instead.")
      return
    }
    const bad = "Enter a full date, a month or a four-digit year."
    const errors = { event: eventDate && !parseDate(eventDate) ? bad : undefined, due: dueDate && !parseDate(dueDate) ? bad : undefined }
    setDateErrors(errors)
    if (errors.event || errors.due) {
      document.getElementById(errors.event ? "edit-event-date" : "edit-due-date")?.focus()
      return
    }
    setBusy(true)
    try {
      await updateMemory({
        data: { id: memory.id, fact: fact.trim(), keywords: keywords.trim(), kind, eventDate: eventDate || null, dueDate: dueDate || null },
      })
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
            <DateField
              key={`${formKey}-event`}
              id="edit-event-date"
              label="Happened"
              description="When it happened or started. Leave empty if there's no date."
              value={eventDate}
              onChange={setEventDate}
              error={dateErrors.event}
            />
            <DateField
              key={`${formKey}-due`}
              id="edit-due-date"
              label="Due"
              description="When it ends, renews or is due. Reminders count down to this."
              value={dueDate}
              onChange={setDueDate}
              error={dateErrors.due}
            />
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
          <AlertDialogAction variant="danger" disabled={busy} data-agent-confirm="Deletes this fact" onClick={() => void confirm()}>
            {busy && <Spinner data-icon="inline-start" />}
            {last.current?.keep ? "Delete the other fact" : "Delete fact"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
