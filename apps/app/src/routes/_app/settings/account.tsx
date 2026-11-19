import * as React from "react"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { EllipsisIcon, FingerprintIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PagePending } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { authClient, authErrorMessage, passkeyCancelled } from "@/lib/auth-client"
import { getAccount, removePasskey } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"

const MIN_PASSWORD = 12

export const Route = createFileRoute("/_app/settings/account")({
  loader: () => getAccount(),
  pendingComponent: () => (
    <PagePending label="Loading your account…">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Account · Recall" }] }),
  component: AccountPage,
})

function AccountPage() {
  const { username } = Route.useLoaderData()
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Account"
        description={
          <>
            Signed in as <span className="font-medium text-foreground">{username}</span>. This applies in every workspace you're in.
          </>
        }
      />
      <div className="grid max-w-3xl gap-6">
        <PasskeysCard />
        <PasswordCard username={username} />
      </div>
    </div>
  )
}

type Passkey = Awaited<ReturnType<typeof getAccount>>["passkeys"][number]

function PasskeysCard() {
  const { passkeys } = Route.useLoaderData()
  const router = useRouter()
  const [adding, setAdding] = React.useState(false)
  const [removing, setRemoving] = React.useState<Passkey | null>(null)
  const lastRemoving = React.useRef<Passkey | null>(null)
  if (removing) lastRemoving.current = removing
  const [busy, setBusy] = React.useState(false)
  // Read after mount so the server and the first client render match.
  const [supported, setSupported] = React.useState(true)
  React.useEffect(() => setSupported(typeof window.PublicKeyCredential === "function"), [])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Passkeys</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!supported && (
          <Alert>
            <AlertTitle>This browser can't use passkeys</AlertTitle>
            <AlertDescription>Open Recall in a current Safari, Chrome, Edge or Firefox to add one.</AlertDescription>
          </Alert>
        )}
        {passkeys.length === 0 ? (
          <Empty className="rounded-lg border border-dashed py-8">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FingerprintIcon />
              </EmptyMedia>
              <EmptyTitle>No passkeys yet</EmptyTitle>
              <EmptyDescription>Add one on each device you use. Your password keeps working.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="overflow-x-auto rounded-lg shadow-(--shadow-border)">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Name</TableHead>
                  <TableHead className="max-sm:hidden">Added</TableHead>
                  <TableHead className="w-12 ps-0 pointer-coarse:w-14" aria-label="Actions" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {passkeys.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="max-w-[16rem]">
                      <div className="flex items-center gap-2">
                        <span className="min-w-0 font-medium wrap-anywhere whitespace-normal">{p.name || "Passkey"}</span>
                        {p.backedUp && <Badge variant="outline">Synced</Badge>}
                      </div>
                    </TableCell>
                    <TableCell className="text-caption tabular-nums max-sm:hidden" title={p.createdAt ? fullDate(p.createdAt) : undefined}>
                      {p.createdAt ? ago(p.createdAt) : "Unknown"}
                    </TableCell>
                    <TableCell className="ps-0 text-end">
                      <DropdownMenu>
                        <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${p.name || "Passkey"}`} />}>
                          <EllipsisIcon aria-hidden="true" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuGroup>
                            <DropdownMenuItem variant="destructive" onSelect={() => setRemoving(p)}>
                              <Trash2Icon aria-hidden="true" />
                              Remove passkey
                            </DropdownMenuItem>
                          </DropdownMenuGroup>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
      <CardFooter>
        <Button variant="outline" disabled={!supported} onClick={() => setAdding(true)}>
          <PlusIcon data-icon="inline-start" />
          Add a passkey
        </Button>
      </CardFooter>

      <AddPasskeyDialog open={adding} onOpenChange={setAdding} onAdded={() => void router.invalidate()} />

      <AlertDialog open={removing !== null} onOpenChange={(o) => !o && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove “{lastRemoving.current?.name || "Passkey"}”?</AlertDialogTitle>
            <AlertDialogDescription>It stops working for Recall right away. Also delete it from your device or password manager so it doesn't keep offering it.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              data-agent-confirm={`Removes the passkey “${lastRemoving.current?.name || "Passkey"}”`}
              disabled={busy}
              onClick={async () => {
                if (!removing) return
                setBusy(true)
                try {
                  await removePasskey({ data: { id: removing.id } })
                  toast.add({ title: "Passkey removed", type: "success" })
                  setRemoving(null)
                  void router.invalidate()
                } catch (err) {
                  toast.add({ title: "Couldn't remove the passkey", description: errorMessage(err), type: "error" })
                } finally {
                  setBusy(false)
                }
              }}
            >
              {busy && <Spinner data-icon="inline-start" />}
              Remove passkey
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}

// A name that tells devices apart in the list: "Mac", "iPhone", "Windows"...
function deviceName() {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return "iPhone"
  if (/iPad/.test(ua)) return "iPad"
  if (/Android/.test(ua)) return "Android"
  if (/Mac/.test(ua)) return "Mac"
  if (/Windows/.test(ua)) return "Windows"
  return "Passkey"
}

function AddPasskeyDialog({ open, onOpenChange, onAdded }: { open: boolean; onOpenChange: (o: boolean) => void; onAdded: () => void }) {
  const [name, setName] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => {
    if (open) {
      setName(deviceName())
      setError(undefined)
    }
  }, [open])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      const res = await authClient.passkey.addPasskey({ name: name.trim() || deviceName() })
      if (res?.error) {
        if (!passkeyCancelled(res.error)) setError(authErrorMessage(res.error, "Your device didn't create the passkey."))
        return
      }
      toast.add({ title: "Passkey added", description: "Next time, choose “Log in with a passkey”.", type: "success" })
      onOpenChange(false)
      onAdded()
    } catch (err) {
      if (!passkeyCancelled({ message: errorMessage(err) })) setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>Add a passkey</DialogTitle>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={error !== undefined}>
              <FieldLabel htmlFor="passkey-name">Name</FieldLabel>
              <Input id="passkey-name" autoFocus maxLength={80} value={name} aria-invalid={error !== undefined} onChange={(e) => setName(e.target.value)} />
              {error ? (
                <FieldError>{error}</FieldError>
              ) : (
                <FieldDescription role="status" className="flex items-center gap-2 empty:hidden">
                  {busy && (
                    <>
                      <Spinner role="presentation" aria-hidden="true" />
                      Waiting for your device…
                    </>
                  )}
                </FieldDescription>
              )}
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
              Add passkey
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PasswordCard({ username }: { username: string }) {
  const [current, setCurrent] = React.useState("")
  const [next, setNext] = React.useState("")
  const [confirm, setConfirm] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [errors, setErrors] = React.useState<{ current?: string; next?: string; confirm?: string }>({})

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const errs: typeof errors = {}
    if (!current) errs.current = "Enter your current password."
    if (next.length < MIN_PASSWORD) errs.next = `Use at least ${MIN_PASSWORD} characters.`
    else if (next === current) errs.next = "Pick a password you haven't used here."
    if (!errs.next && confirm !== next) errs.confirm = "The passwords don't match."
    setErrors(errs)
    if (Object.keys(errs).length) {
      document.getElementById(errs.current ? "current-password" : errs.next ? "new-password" : "confirm-password")?.focus()
      return
    }
    setBusy(true)
    try {
      const res = await authClient.changePassword({ currentPassword: current, newPassword: next, revokeOtherSessions: true })
      if (res.error) {
        const wrong = res.error.code === "INVALID_PASSWORD" || res.error.status === 400
        setErrors(wrong ? { current: "That isn't your current password." } : { next: authErrorMessage(res.error, "Couldn't change the password.") })
        return
      }
      setCurrent("")
      setNext("")
      setConfirm("")
      toast.add({ title: "Password changed", description: "Other devices were signed out.", type: "success" })
    } catch (err) {
      setErrors({ next: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <form noValidate onSubmit={submit} className="contents">
        <CardHeader>
          <CardTitle>Password</CardTitle>
          <CardDescription>Changing it signs you out everywhere else.</CardDescription>
        </CardHeader>
        <CardContent>
          {/* Lets password managers file the new password under the right account. */}
          <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
          <FieldGroup>
            <Field data-invalid={!!errors.current}>
              <FieldLabel htmlFor="current-password">Current password</FieldLabel>
              <Input
                id="current-password"
                type="password"
                autoComplete="current-password"
                value={current}
                aria-invalid={!!errors.current}
                aria-describedby={errors.current ? "current-error" : undefined}
                onChange={(e) => setCurrent(e.target.value)}
              />
              {errors.current && <FieldError id="current-error">{errors.current}</FieldError>}
            </Field>
            <Field data-invalid={!!errors.next}>
              <FieldLabel htmlFor="new-password">New password</FieldLabel>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                value={next}
                aria-invalid={!!errors.next}
                aria-describedby="new-hint"
                onChange={(e) => setNext(e.target.value)}
              />
              {errors.next ? <FieldError id="new-hint">{errors.next}</FieldError> : <FieldDescription id="new-hint">At least {MIN_PASSWORD} characters.</FieldDescription>}
            </Field>
            <Field data-invalid={!!errors.confirm}>
              <FieldLabel htmlFor="confirm-password">Confirm new password</FieldLabel>
              <Input
                id="confirm-password"
                type="password"
                autoComplete="new-password"
                value={confirm}
                aria-invalid={!!errors.confirm}
                aria-describedby={errors.confirm ? "confirm-error" : undefined}
                onChange={(e) => setConfirm(e.target.value)}
              />
              {errors.confirm && <FieldError id="confirm-error">{errors.confirm}</FieldError>}
            </Field>
          </FieldGroup>
        </CardContent>
        <CardFooter>
          <Button type="submit" disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            Change password
          </Button>
        </CardFooter>
      </form>
    </Card>
  )
}
