// Picks the workspace every page acts in. Sits left of the container switcher:
// workspace first, then the container inside it. New workspaces start here too.
import * as React from "react"
import { useNavigate, useRouter } from "@tanstack/react-router"
import { ChevronsUpDownIcon, PlusIcon, SettingsIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { newWorkspace } from "@/lib/fns"
import { errorMessage } from "@/lib/format"
import { picked } from "@/lib/utils"

export type WorkspaceInfo = { id: string; name: string; slug: string; role: "owner" | "admin" | "member" }

const ROLE_LABEL = { owner: "Owner", admin: "Admin", member: "Member" } as const

export function WorkspaceSwitcher({
  value,
  workspaces,
  onChange,
}: {
  value: WorkspaceInfo
  workspaces: WorkspaceInfo[]
  onChange: (id: string) => Promise<void>
}) {
  const navigate = useNavigate()
  const [creating, setCreating] = React.useState(false)
  const [switching, setSwitching] = React.useState(false)

  const pick = async (id: string) => {
    if (id === value.id) return
    setSwitching(true)
    try {
      await onChange(id)
    } catch (err) {
      toast.add({ title: "Couldn't switch workspace", description: errorMessage(err), type: "error" })
    } finally {
      setSwitching(false)
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              className="max-w-[min(14rem,32vw)] min-w-0 shrink-[4] justify-between gap-2 sm:max-w-[min(14rem,38vw)]"
              aria-label={`Workspace: ${value.name}. Change workspace`}
              disabled={switching}
            />
          }
        >
          <span className="text-muted-foreground max-md:hidden">Workspace</span>
          <span className="truncate font-medium" title={value.name}>
            {value.name}
          </span>
          {switching ? <Spinner className="text-muted-foreground" /> : <ChevronsUpDownIcon aria-hidden="true" className="text-muted-foreground" />}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>Workspaces</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={value.id} onValueChange={picked((id) => void pick(id))}>
              {workspaces.map((w) => (
                <DropdownMenuRadioItem key={w.id} value={w.id} closeOnSelect className="gap-2">
                  <span className="min-w-0 flex-1 truncate" title={w.name}>
                    {w.name}
                  </span>
                  <span className="shrink-0 text-caption text-muted-foreground">{ROLE_LABEL[w.role]}</span>
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem onSelect={() => void navigate({ to: "/settings/members" })}>
              <SettingsIcon aria-hidden="true" />
              Workspace settings
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setCreating(true)}>
              <PlusIcon aria-hidden="true" />
              New workspace
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <NewWorkspaceDialog open={creating} onOpenChange={setCreating} />
    </>
  )
}

function NewWorkspaceDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter()
  const navigate = useNavigate()
  const [name, setName] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => {
    if (open) {
      setName("")
      setError(undefined)
    }
  }, [open])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) {
      setError("Name the workspace, like a team or a project.")
      return
    }
    setBusy(true)
    try {
      const ws = await newWorkspace({ data: { name: name.trim() } })
      onOpenChange(false)
      await navigate({ to: "/", search: {} })
      await router.invalidate()
      toast.add({ title: `Created ${ws.name}`, description: "You're its owner. Invite people from Members.", type: "success" })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>New workspace</DialogTitle>
            <DialogDescription>A workspace has its own memories, containers, members and API keys. Nothing is shared between workspaces.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={error !== undefined}>
              <FieldLabel htmlFor="workspace-name">Name</FieldLabel>
              <Input
                id="workspace-name"
                autoFocus
                placeholder="Vorneo team"
                value={name}
                maxLength={80}
                aria-invalid={error !== undefined}
                onChange={(e) => setName(e.target.value)}
              />
              {error ? <FieldError>{error}</FieldError> : <FieldDescription>Members see this name in their workspace list.</FieldDescription>}
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner data-icon="inline-start" />}
              Create workspace
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
