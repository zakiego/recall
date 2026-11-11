import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PageHeaderSkeleton, PagePending, TableSkeleton } from "@/components/page-pending"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { createKey, getKeys, revokeKey } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { AppError } from "@/components/app-error"

export const Route = createFileRoute("/_app/settings/keys")({
  loader: () => getKeys(),
  pendingComponent: () => (
    <PagePending label="Loading keys…">
      <PageHeaderSkeleton description="w-80" action />
      <TableSkeleton
        rows={3}
        columns={[
          {},
          { className: "max-sm:hidden", short: "w-28" },
          { className: "max-sm:hidden", short: "w-24" },
          { short: "w-20" },
          { className: "w-24", short: "h-7 w-16", bare: true, align: "end" },
        ]}
      />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "API keys · Recall" }] }),
  component: KeysPage,
})

type Key = Awaited<ReturnType<typeof getKeys>>["keys"][number]

function KeysPage() {
  const { keys, canManage } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const workspace = viewer.workspace?.name ?? "this workspace"
  const router = useRouter()
  const [creating, setCreating] = React.useState(false)
  const [revoking, setRevoking] = React.useState<Key | null>(null)
  const [revokeBusy, setRevokeBusy] = React.useState(false)
  // Keep the last key on screen while the dialog animates out.
  const lastRevoking = React.useRef<Key | null>(null)
  if (revoking) lastRevoking.current = revoking

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="API keys"
        description={
          <>
            Keys let agents use the REST API and the MCP server. Send one as a Bearer token. A key reads and writes every container in{" "}
            <span className="font-medium text-foreground">{workspace}</span> and nothing outside it.
          </>
        }
        actions={
          canManage && (
            <Button onClick={() => setCreating(true)}>
              <PlusIcon data-icon="inline-start" />
              New key
            </Button>
          )
        }
      />

      {!canManage && (
        <Alert>
          <AlertTitle>Only owners and admins manage keys</AlertTitle>
          <AlertDescription>You can see which keys exist. Ask an admin of {workspace} for a key or to revoke one.</AlertDescription>
        </Alert>
      )}

      {keys.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <KeyRoundIcon />
            </EmptyMedia>
            <EmptyTitle>No keys yet</EmptyTitle>
            <EmptyDescription>
              {canManage ? "Create one per agent or app, so you can revoke one without breaking the others." : `Nobody has made a key for ${workspace} yet.`}
            </EmptyDescription>
          </EmptyHeader>
          {canManage && (
            <EmptyContent>
              <Button onClick={() => setCreating(true)}>
                <PlusIcon data-icon="inline-start" />
                New key
              </Button>
            </EmptyContent>
          )}
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
                    {k.createdBy && <span className="text-muted-foreground"> by {k.createdBy}</span>}
                  </TableCell>
                  <TableCell className="text-caption tabular-nums" title={k.lastUsedAt ? fullDate(k.lastUsedAt) : undefined}>
                    {k.lastUsedAt ? ago(k.lastUsedAt) : "Never"}
                  </TableCell>
                  <TableCell className="text-end">
                    {canManage && !k.revokedAt && (
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

      <ConnectHint />

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

// Setup snippets live on the Connect page; this just points there.
function ConnectHint() {
  return (
    <p className="text-sm text-muted-foreground">
      Wiring up an agent? <Link to="/connect" className="font-medium text-foreground underline underline-offset-4">Connect</Link> has the
      config for Claude Code, Claude Desktop, other MCP clients and REST.
    </p>
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
                {error ? <FieldError>{error}</FieldError> : <FieldDescription>Everyone in the workspace sees this name in the list.</FieldDescription>}
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
