import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { EllipsisIcon, LinkIcon, PlusIcon, TriangleAlertIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { PageHeader } from "@/components/page-header"
import { SimpleSelect } from "@/components/simple-select"
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { PageHeaderSkeleton, PagePending, TableSkeleton } from "@/components/page-pending"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/toast"
import { createInviteLink, getMembers, removeWorkspaceMember, revokeInviteLink, setMemberRole } from "@/lib/fns"
import { ago, errorMessage, fullDate } from "@/lib/format"

type Role = "owner" | "admin" | "member"
const ROLE_LABEL: Record<Role, string> = { owner: "Owner", admin: "Admin", member: "Member" }
const ROLE_HINT: Record<Role, string> = {
  owner: "Everything, including other owners.",
  admin: "Members, invites and API keys. Not owners.",
  member: "Read and write memories. Sees keys, can't make them.",
}

export const Route = createFileRoute("/_app/settings/members")({
  loader: () => getMembers(),
  pendingComponent: () => (
    <PagePending label="Loading members…">
      <PageHeaderSkeleton description="w-80" action />
      <TableSkeleton
        rows={3}
        columns={[
          { sub: true },
          { className: "w-40", short: "h-8 w-28" },
          { className: "max-sm:hidden", short: "w-24" },
          { className: "w-12", short: "size-4", bare: true },
        ]}
      />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Members · Recall" }] }),
  component: MembersPage,
})

type Data = Awaited<ReturnType<typeof getMembers>>
type Member = Data["members"][number]

function MembersPage() {
  const data = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const workspace = viewer.workspace?.name ?? "this workspace"
  const router = useRouter()
  const navigate = useNavigate()
  const [inviting, setInviting] = React.useState(false)
  const [removing, setRemoving] = React.useState<Member | null>(null)
  const lastRemoving = React.useRef<Member | null>(null)
  if (removing) lastRemoving.current = removing
  const [busy, setBusy] = React.useState<string | null>(null)
  const owners = data.members.filter((m) => m.role === "owner").length

  const changeRole = async (m: Member, role: Role) => {
    setBusy(m.memberId)
    try {
      await setMemberRole({ data: { memberId: m.memberId, role } })
      toast.add({ title: `${m.displayUsername ?? m.username} is now ${ROLE_LABEL[role].toLowerCase()}`, type: "success" })
      await router.invalidate()
    } catch (err) {
      toast.add({ title: "Couldn't change the role", description: errorMessage(err), type: "error" })
    } finally {
      setBusy(null)
    }
  }

  const roleOptions = (m: Member) =>
    (["owner", "admin", "member"] as const).filter((r) => data.myRole === "owner" || (r !== "owner" && m.role !== "owner")).map((r) => ({ value: r, label: ROLE_LABEL[r] }))

  const canEditRole = (m: Member) => data.canManage && (data.myRole === "owner" || m.role !== "owner")
  const canRemove = (m: Member) => m.userId === data.myUserId || (data.canManage && (data.myRole === "owner" || m.role !== "owner"))

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Members"
        description={
          <>
            Everyone in <span className="font-medium text-foreground">{workspace}</span> sees the same memories, containers and keys. People join
            with an invite link.
          </>
        }
        actions={
          data.canManage && (
            <Button onClick={() => setInviting(true)}>
              <PlusIcon data-icon="inline-start" />
              Invite
            </Button>
          )
        }
      />

      <div className="overflow-x-auto rounded-xl bg-card shadow-(--shadow-border)">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Member</TableHead>
              <TableHead className="w-40">Role</TableHead>
              <TableHead className="max-sm:hidden">Joined</TableHead>
              <TableHead className="w-12 ps-0 pointer-coarse:w-14" aria-label="Actions" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.members.map((m) => {
              const name = m.displayUsername ?? m.username ?? m.name
              const you = m.userId === data.myUserId
              const lastOwner = m.role === "owner" && owners <= 1
              return (
                <TableRow key={m.memberId}>
                  <TableCell className="max-w-[16rem]">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium" title={name}>
                        {name}
                      </span>
                      {you && <Badge variant="outline">You</Badge>}
                    </div>
                  </TableCell>
                  <TableCell>
                    {canEditRole(m) && !lastOwner ? (
                      <SimpleSelect
                        size="sm"
                        className="w-32"
                        aria-label={`Role for ${name}`}
                        value={m.role}
                        options={roleOptions(m)}
                        disabled={busy === m.memberId}
                        onChange={(r) => void changeRole(m, r as Role)}
                      />
                    ) : (
                      <span className="text-sm" title={lastOwner ? "A workspace keeps at least one owner." : undefined}>
                        {ROLE_LABEL[m.role]}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-caption tabular-nums max-sm:hidden" title={fullDate(m.joinedAt)}>
                    {ago(m.joinedAt)}
                  </TableCell>
                  <TableCell className="ps-0 text-end">
                    {canRemove(m) && !lastOwner && (
                      <DropdownMenu>
                        <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${name}`} />}>
                          <EllipsisIcon aria-hidden="true" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuGroup>
                            <DropdownMenuItem variant="destructive" onSelect={() => setRemoving(m)}>
                              {you ? "Leave workspace" : "Remove from workspace"}
                            </DropdownMenuItem>
                          </DropdownMenuGroup>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      {data.canManage && <PendingInvites invites={data.invites} />}

      <InviteDialog open={inviting} onOpenChange={setInviting} myRole={data.myRole} workspace={workspace} onCreated={() => void router.invalidate()} />

      <AlertDialog open={removing !== null} onOpenChange={(o) => !o && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {lastRemoving.current?.userId === data.myUserId ? `Leave ${workspace}?` : `Remove ${lastRemoving.current?.displayUsername ?? lastRemoving.current?.username}?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {lastRemoving.current?.userId === data.myUserId
                ? "You lose access to its memories right away. API keys and invite links you made here are revoked. Getting back in takes a new invite link."
                : "They lose access to this workspace's memories right away. API keys and invite links they made here are revoked. Their account stays."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              data-agent-confirm="Removes this member from the workspace"
              disabled={busy !== null}
              onClick={async () => {
                if (!removing) return
                setBusy(removing.memberId)
                try {
                  const res = await removeWorkspaceMember({ data: { memberId: removing.memberId } })
                  setRemoving(null)
                  if (res.left) {
                    await navigate({ to: "/", search: {} })
                    await router.invalidate()
                    toast.add({ title: `You left ${workspace}`, type: "success" })
                  } else {
                    toast.add({ title: "Member removed", type: "success" })
                    await router.invalidate()
                  }
                } catch (err) {
                  toast.add({ title: "Couldn't remove them", description: errorMessage(err), type: "error" })
                } finally {
                  setBusy(null)
                }
              }}
            >
              {busy !== null && <Spinner data-icon="inline-start" />}
              {lastRemoving.current?.userId === data.myUserId ? "Leave" : "Remove"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function PendingInvites({ invites }: { invites: Data["invites"] }) {
  const router = useRouter()
  const [busy, setBusy] = React.useState<string | null>(null)
  return (
    <section aria-labelledby="pending-invites" className="flex flex-col gap-3">
      <h2 id="pending-invites" className="font-heading text-heading">
        Open invite links
      </h2>
      {invites.length === 0 ? (
        <p className="text-sm text-muted-foreground">None. Links you create show here until someone uses them, they expire, or you revoke them.</p>
      ) : (
        <div className="overflow-x-auto rounded-xl bg-card shadow-(--shadow-border)">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Joins as</TableHead>
                <TableHead className="max-sm:hidden">Created</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead className="w-24" aria-label="Actions" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {invites.map((inv) => (
                <TableRow key={inv.id}>
                  <TableCell>{ROLE_LABEL[inv.role]}</TableCell>
                  <TableCell className="text-caption tabular-nums max-sm:hidden" title={fullDate(inv.createdAt)}>
                    {ago(inv.createdAt)}
                    {inv.createdBy && <span className="text-muted-foreground"> by {inv.createdBy}</span>}
                  </TableCell>
                  <TableCell className="text-caption tabular-nums" title={fullDate(inv.expiresAt)}>
                    {fullDate(inv.expiresAt)}
                  </TableCell>
                  <TableCell className="text-end">
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy === inv.id}
                      onClick={async () => {
                        setBusy(inv.id)
                        try {
                          await revokeInviteLink({ data: { id: inv.id } })
                          toast.add({ title: "Invite link revoked", type: "success" })
                          await router.invalidate()
                        } catch (err) {
                          toast.add({ title: "Couldn't revoke the link", description: errorMessage(err), type: "error" })
                        } finally {
                          setBusy(null)
                        }
                      }}
                    >
                      Revoke
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}

function InviteDialog({
  open,
  onOpenChange,
  myRole,
  workspace,
  onCreated,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  myRole: Role
  workspace: string
  onCreated: () => void
}) {
  const [role, setRole] = React.useState<Role>("member")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const [created, setCreated] = React.useState<{ url: string; role: Role; expiresAt: number } | null>(null)

  React.useEffect(() => {
    if (open) {
      setRole("member")
      setError(undefined)
      setCreated(null)
    }
  }, [open])

  const options = (["member", "admin", "owner"] as const).filter((r) => r !== "owner" || myRole === "owner").map((r) => ({ value: r, label: ROLE_LABEL[r] }))

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      const res = await createInviteLink({ data: { role } })
      setCreated({ url: res.url, role: res.role, expiresAt: res.expiresAt })
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
              <DialogTitle>Invite link for {workspace}</DialogTitle>
              <DialogDescription>
                Send it to one person. They pick a username and password and join as {ROLE_LABEL[created.role].toLowerCase()}.
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-start gap-2 rounded-lg bg-muted p-3">
              <LinkIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <code className="min-w-0 flex-1 font-mono text-caption break-all select-all">{created.url}</code>
              <CopyButton text={created.url} />
            </div>
            <Alert variant="warning">
              <TriangleAlertIcon />
              <AlertTitle>You won't see this link again</AlertTitle>
              <AlertDescription>
                Whoever opens it first can join. It works once and expires {fullDate(created.expiresAt)}. Revoke it from this page if it goes to the wrong
                person.
              </AlertDescription>
            </Alert>
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-6">
            <DialogHeader>
              <DialogTitle>Invite to {workspace}</DialogTitle>
              <DialogDescription>Creates a single-use link that lasts 7 days.</DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="invite-role">Joins as</FieldLabel>
                <SimpleSelect id="invite-role" value={role} options={options} onChange={(r) => setRole(r as Role)} />
                <FieldDescription>{ROLE_HINT[role]}</FieldDescription>
              </Field>
              {error && (
                <Alert variant="destructive" role="alert" className="enter-in">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy && <Spinner data-icon="inline-start" />}
                Create link
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
