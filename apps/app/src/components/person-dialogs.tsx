import * as React from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { mergePerson, removePerson, savePerson } from "@/lib/fns"
import { errorMessage, plural } from "@/lib/format"
import { cn } from "@/lib/utils"

export type EditablePerson = {
  id: string
  name: string
  aliases: string[]
  role: string | null
  organization: string | null
  summary: string
  emails: string[]
  handles: string[]
}

const splitList = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean)

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function EditPersonDialog({
  person,
  open,
  onOpenChange,
  onSaved,
}: {
  person: EditablePerson
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [form, setForm] = React.useState({ name: "", aliases: "", role: "", organization: "", summary: "", emails: "", handles: "" })
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<{ field: "name" | "emails" | "form"; message: string }>()

  React.useEffect(() => {
    if (!open) return
    setForm({
      name: person.name,
      aliases: person.aliases.join(", "),
      role: person.role ?? "",
      organization: person.organization ?? "",
      summary: person.summary,
      emails: person.emails.join(", "),
      handles: person.handles.join(", "),
    })
    setError(undefined)
  }, [open, person])

  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setForm((f) => ({ ...f, [key]: e.target.value }))

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) return setError({ field: "name", message: "A person needs a name. To remove them, delete them instead." })
    const emails = splitList(form.emails)
    const bad = emails.find((m) => !EMAIL.test(m))
    if (bad) return setError({ field: "emails", message: `“${bad}” isn't an email address.` })
    setBusy(true)
    try {
      await savePerson({
        data: {
          id: person.id,
          name: form.name.trim(),
          aliases: splitList(form.aliases),
          role: form.role.trim() || null,
          organization: form.organization.trim() || null,
          summary: form.summary.trim(),
          emails,
          handles: splitList(form.handles),
        },
      })
      onSaved()
      onOpenChange(false)
    } catch (err) {
      setError({ field: "form", message: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>Edit person</DialogTitle>
            <DialogDescription>
              After this, extraction keeps the name, role, organization and summary you set. It still adds aliases, contacts and new facts.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={error?.field === "name"}>
              <FieldLabel htmlFor="person-name">Name</FieldLabel>
              <Input id="person-name" value={form.name} maxLength={120} autoComplete="off" aria-invalid={error?.field === "name"} onChange={set("name")} />
              {error?.field === "name" && <FieldError>{error.message}</FieldError>}
            </Field>
            <Field>
              <FieldLabel htmlFor="person-aliases">Aliases</FieldLabel>
              <Input id="person-aliases" value={form.aliases} autoComplete="off" onChange={set("aliases")} />
              <FieldDescription>Other names for them, separated by commas. Facts that use these names link here.</FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="person-role">Role</FieldLabel>
                <Input id="person-role" value={form.role} maxLength={120} autoComplete="off" placeholder="Product designer" onChange={set("role")} />
              </Field>
              <Field>
                <FieldLabel htmlFor="person-org">Organization</FieldLabel>
                <Input id="person-org" value={form.organization} maxLength={120} autoComplete="off" onChange={set("organization")} />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="person-summary">Summary</FieldLabel>
              <Textarea id="person-summary" value={form.summary} maxLength={600} rows={2} onChange={set("summary")} />
            </Field>
            <Field data-invalid={error?.field === "emails"}>
              <FieldLabel htmlFor="person-emails">Emails</FieldLabel>
              <Input
                id="person-emails"
                value={form.emails}
                inputMode="email"
                autoComplete="off"
                spellCheck={false}
                aria-invalid={error?.field === "emails"}
                onChange={set("emails")}
              />
              {error?.field === "emails" ? <FieldError>{error.message}</FieldError> : <FieldDescription>Separated by commas.</FieldDescription>}
            </Field>
            <Field>
              <FieldLabel htmlFor="person-handles">Handles</FieldLabel>
              <Input id="person-handles" value={form.handles} autoComplete="off" spellCheck={false} placeholder="@clara" onChange={set("handles")} />
            </Field>
          </FieldGroup>
          {error?.field === "form" && (
            <Alert variant="destructive">
              <AlertTitle>Couldn't save</AlertTitle>
              <AlertDescription>{error.message}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner data-icon="inline-start" />}
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type Other = { id: string; name: string; role: string | null; organization: string | null; factCount: number }

// Picks someone else in the container, then confirms. The person on the page is kept.
export function MergePersonDialog({
  person,
  others,
  open,
  onOpenChange,
  onMerged,
}: {
  person: { id: string; name: string }
  others: Other[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onMerged: (dropped: Other) => void
}) {
  const [picked, setPicked] = React.useState<Other | null>(null)
  // Only a step change animates; the first step arrives with the dialog itself.
  const [stepped, setStepped] = React.useState(false)
  const pick = (o: Other | null) => {
    setStepped(true)
    setPicked(o)
  }
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => {
    if (!open) return
    setPicked(null)
    setStepped(false)
    setError(undefined)
  }, [open])

  const merge = async () => {
    if (!picked) return
    setBusy(true)
    setError(undefined)
    try {
      await mergePerson({ data: { keepId: person.id, dropId: picked.id } })
      onMerged(picked)
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {/* The step swaps in place; a short rise keeps it from teleporting (reduced motion: none). */}
        <div key={picked ? "confirm" : "pick"} className={cn("grid gap-5", stepped && "enter-in")}>
        <DialogHeader>
          <DialogTitle>{picked ? `Merge ${picked.name} into ${person.name}?` : `Merge someone into ${person.name}`}</DialogTitle>
          <DialogDescription>
            {picked
              ? `${picked.name}'s facts, aliases and contacts move here, “${picked.name}” becomes an alias, and their page goes away. Their role, organization and summary are dropped where ${person.name} already has one. This can't be undone.`
              : "Pick the duplicate. Use this when the same person shows up twice, like “Clara” and “Clara Simmons”."}
          </DialogDescription>
        </DialogHeader>
        {!picked ? (
          others.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nobody else in this container to merge with.</p>
          ) : (
            <Command className="rounded-lg shadow-(--shadow-border)">
              <CommandInput placeholder="Find a person" />
              <CommandList>
                <CommandEmpty>Nobody by that name.</CommandEmpty>
                <CommandGroup>
                  {others.map((o) => (
                    <CommandItem key={o.id} value={`${o.name} ${o.role ?? ""} ${o.organization ?? ""} ${o.id}`} onSelect={() => pick(o)}>
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="truncate">{o.name}</span>
                        {(o.role || o.organization) && (
                          <span className="truncate text-caption text-muted-foreground">{[o.role, o.organization].filter(Boolean).join(" · ")}</span>
                        )}
                      </span>
                      <span className="text-caption text-muted-foreground tabular-nums">{plural(o.factCount, "fact")}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              </CommandList>
            </Command>
          )
        ) : (
          error && (
            <Alert variant="destructive">
              <AlertTitle>Couldn't merge</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )
        )}
        </div>
        <DialogFooter>
          {picked ? (
            <>
              <Button variant="outline" onClick={() => pick(null)}>
                Pick someone else
              </Button>
              <Button disabled={busy} onClick={() => void merge()}>
                {busy && <Spinner data-icon="inline-start" />}
                Merge
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function DeletePersonDialog({
  person,
  open,
  onOpenChange,
  onDeleted,
}: {
  person: { id: string; name: string }
  open: boolean
  onOpenChange: (open: boolean) => void
  onDeleted: () => void
}) {
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  const confirm = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await removePerson({ data: { id: person.id } })
      onOpenChange(false)
      onDeleted()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {person.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Their page and links go away. The facts that mention them stay. If new content names them again, they come back as a new person.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertTitle>Couldn't delete them</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Keep them</AlertDialogCancel>
          <AlertDialogAction variant="danger" disabled={busy} onClick={() => void confirm()}>
            {busy && <Spinner data-icon="inline-start" />}
            Delete person
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
