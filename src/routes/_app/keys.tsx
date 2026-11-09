import * as React from "react"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { KeyRoundIcon, PlusIcon, TriangleAlertIcon } from "lucide-react"

import { CopyButton } from "@/components/copy-button"
import { PageHeader } from "@/components/page-header"
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
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { createKey, getKeys, revokeKey } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { AppError } from "@/components/app-error"

export const Route = createFileRoute("/_app/keys")({
  loader: () => getKeys(),
  pendingComponent: () => (
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading keys">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-48 w-full rounded-xl" />
    </div>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "API keys · Recall" }] }),
  component: KeysPage,
})

type Key = Awaited<ReturnType<typeof getKeys>>[number]

function KeysPage() {
  const keys = Route.useLoaderData()
  const router = useRouter()
  const [creating, setCreating] = React.useState(false)
  const [revoking, setRevoking] = React.useState<Key | null>(null)
  const [revokeBusy, setRevokeBusy] = React.useState(false)
  // Keep the last key on screen while the dialog animates out.
  const lastRevoking = React.useRef<Key | null>(null)
  if (revoking) lastRevoking.current = revoking
  const active = keys.filter((k) => !k.revokedAt)

  return (
    <div className="page-in flex flex-col gap-6">
      <PageHeader
        title="API keys"
        description="Keys let agents use the REST API and the MCP server. Send one as a Bearer token. Every key can read and write every container."
        actions={
          <Button onClick={() => setCreating(true)}>
            <PlusIcon data-icon="inline-start" />
            New key
          </Button>
        }
      />

      {keys.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <KeyRoundIcon />
            </EmptyMedia>
            <EmptyTitle>No keys yet</EmptyTitle>
            <EmptyDescription>Create one per agent or app, so you can revoke one without breaking the others.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => setCreating(true)}>
              <PlusIcon data-icon="inline-start" />
              New key
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <div className="overflow-x-auto rounded-xl bg-card shadow-(--shadow-border)">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead className="max-sm:hidden">Key</TableHead>
                <TableHead className="max-sm:hidden">Created</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead className="w-24" aria-label="Actions" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.map((k) => (
                <TableRow key={k.id} className={cn(k.revokedAt && "text-muted-foreground")}>
                  <TableCell className="max-w-[16rem]">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium" title={k.name}>
                        {k.name}
                      </span>
                      {k.revokedAt && <Badge variant="outline">Revoked</Badge>}
                    </div>
                  </TableCell>
                  <TableCell className="max-sm:hidden">
                    <code className="font-mono text-caption">{k.prefix}…</code>
                  </TableCell>
                  <TableCell className="text-caption tabular-nums max-sm:hidden" title={fullDate(k.createdAt)}>
                    {ago(k.createdAt)}
                  </TableCell>
                  <TableCell className="text-caption tabular-nums" title={k.lastUsedAt ? fullDate(k.lastUsedAt) : undefined}>
                    {k.lastUsedAt ? ago(k.lastUsedAt) : "Never"}
                  </TableCell>
                  <TableCell className="text-end">
                    {!k.revokedAt && (
                      <Button variant="ghost" size="sm" onClick={() => setRevoking(k)}>
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {active.length > 0 && <ConnectSnippet />}

      <CreateKeyDialog open={creating} onOpenChange={setCreating} onCreated={() => void router.invalidate()} />

      <AlertDialog open={revoking !== null} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke “{lastRevoking.current?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Anything using this key gets 401 from the next request on. This can't be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              disabled={revokeBusy}
              onClick={async () => {
                if (!revoking) return
                setRevokeBusy(true)
                try {
                  await revokeKey({ data: { id: revoking.id } })
                  toast.add({ title: `Revoked ${revoking.name}`, type: "success" })
                  setRevoking(null)
                  void router.invalidate()
                } catch (err) {
                  toast.add({ title: "Couldn't revoke the key", description: errorMessage(err), type: "error" })
                } finally {
                  setRevokeBusy(false)
                }
              }}
            >
              {revokeBusy && <Spinner data-icon="inline-start" />}
              Revoke key
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ConnectSnippet() {
  // Read after mount so the server and client render the same first frame.
  const [origin, setOrigin] = React.useState("https://recall.zakiego.com")
  React.useEffect(() => setOrigin(window.location.origin), [])
  const cmd = `claude mcp add --transport http recall ${origin}/mcp --header "Authorization: Bearer rcl_..."`
  return (
    <section aria-labelledby="connect" className="flex flex-col gap-2">
      <h2 id="connect" className="font-heading text-heading">
        Connect Claude Code
      </h2>
      <p className="text-sm text-muted-foreground">Replace rcl_... with a key. The tools are add_memory, search_memories, get_profile and list_memories.</p>
      <Card size="sm">
        <CardContent className="flex items-start gap-2">
          <code className="min-w-0 flex-1 font-mono text-caption break-all">{cmd}</code>
          <CopyButton text={cmd} />
        </CardContent>
      </Card>
    </section>
  )
}

function CreateKeyDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: () => void }) {
  const [name, setName] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const [created, setCreated] = React.useState<{ key: string; name: string } | null>(null)

  React.useEffect(() => {
    if (open) {
      setName("")
      setError(undefined)
      setCreated(null)
    }
  }, [open])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) {
      setError("Name the key after the agent or app that will use it.")
      return
    }
    setBusy(true)
    try {
      const res = await createKey({ data: { name: name.trim() } })
      setCreated({ key: res.key, name: res.name })
      onCreated()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {created ? (
          <div className="flex animate-[enter-in_200ms_var(--ease-emphasized)] flex-col gap-6">
            <DialogHeader>
              <DialogTitle>Key for {created.name}</DialogTitle>
              <DialogDescription>Copy it now and store it where the agent can read it.</DialogDescription>
            </DialogHeader>
            <div className="flex items-start gap-2 rounded-lg bg-muted p-3">
              <code className="min-w-0 flex-1 font-mono text-caption break-all select-all">{created.key}</code>
              <CopyButton text={created.key} />
            </div>
            <Alert variant="warning">
              <TriangleAlertIcon />
              <AlertTitle>You won't see this key again</AlertTitle>
              <AlertDescription>Recall stores only its hash. If you lose it, revoke it and make a new one.</AlertDescription>
            </Alert>
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-6">
            <DialogHeader>
              <DialogTitle>New API key</DialogTitle>
              <DialogDescription>One key per agent makes revoking painless.</DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field data-invalid={error !== undefined}>
                <FieldLabel htmlFor="key-name">Name</FieldLabel>
                <Input
                  id="key-name"
                  autoFocus
                  placeholder="claude-code-laptop"
                  value={name}
                  maxLength={80}
                  aria-invalid={error !== undefined}
                  onChange={(e) => setName(e.target.value)}
                />
                {error ? <FieldError>{error}</FieldError> : <FieldDescription>Only you see this. It shows up in the list.</FieldDescription>}
              </Field>
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <Spinner data-icon="inline-start" />}
                Create key
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
