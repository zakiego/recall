// Where an invite link lands. A visitor without an account picks a username and
// password; a signed-in user joins with one click. Either way the link is single use.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { LinkIcon } from "lucide-react"

import { RecallMark } from "@/components/app-sidebar"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { acceptInvite, acceptInviteNewAccount, getInvite } from "@/lib/fns"
import { errorMessage, fullDate } from "@/lib/format"

const ROLE = { owner: "an owner", admin: "an admin", member: "a member" } as const
const MIN_PASSWORD = 12

export const Route = createFileRoute("/invite/$token")({
  loader: ({ params }) => getInvite({ data: { token: params.token } }),
  head: () => ({ meta: [{ title: "Join a workspace · Recall" }] }),
  component: InvitePage,
  errorComponent: ({ error }) => (
    <Frame>
      <Alert variant="destructive">
        <AlertTitle>This invite couldn't load</AlertTitle>
        <AlertDescription>{errorMessage(error)}</AlertDescription>
      </Alert>
    </Frame>
  ),
})

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative flex min-h-svh flex-col items-center justify-center bg-background px-4 py-12">
      <div className="absolute end-4 top-4">
        <ThemeToggle />
      </div>
      <main className="stagger-in relative flex w-full max-w-sm flex-col gap-6">
        <span
          aria-hidden="true"
          className="mx-auto flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-[0_1px_2px_oklch(0_0_0/0.12),inset_0_1px_0_oklch(1_0_0/0.15)]"
        >
          <RecallMark className="size-7" />
        </span>
        {children}
      </main>
    </div>
  )
}

function InvitePage() {
  const invite = Route.useLoaderData()
  const { token } = Route.useParams()
  const router = useRouter()

  const enter = async () => {
    await router.invalidate()
    await router.navigate({ to: "/", search: {} })
  }

  if (!invite.ok) {
    return (
      <Frame>
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <LinkIcon />
            </EmptyMedia>
            <EmptyTitle>Can't use this link</EmptyTitle>
            <EmptyDescription>{invite.problem} Invite links work once and last 7 days.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" render={<Link to={invite.signedInAs ? "/" : "/login"} search={{}} />}>
              {invite.signedInAs ? "Go to Recall" : "Log in"}
            </Button>
          </EmptyContent>
        </Empty>
      </Frame>
    )
  }

  return (
    <Frame>
      <div className="flex flex-col gap-1.5 text-center">
        <h1 className="font-heading text-title text-balance">Join {invite.workspace}</h1>
        <p className="text-sm text-pretty text-muted-foreground">
          You're invited as {ROLE[invite.role]}. The link works once, until {fullDate(invite.expiresAt)}.
        </p>
      </div>
      {invite.alreadyMember ? (
        <Card>
          <CardHeader>
            <CardTitle>You're already in {invite.workspace}</CardTitle>
            <CardDescription>Signed in as {invite.signedInAs}. Nothing to accept, and the link stays unused for someone else.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button className="w-full" onClick={() => void enter()}>
              Go to Recall
            </Button>
          </CardContent>
        </Card>
      ) : invite.signedInAs ? (
        <JoinAsUser token={token} username={invite.signedInAs} workspace={invite.workspace} onJoined={enter} />
      ) : (
        <NewAccountForm token={token} onJoined={enter} />
      )}
    </Frame>
  )
}

function JoinAsUser({ token, username, workspace, onJoined }: { token: string; username: string; workspace: string; onJoined: () => Promise<void> }) {
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Join as {username}</CardTitle>
        <CardDescription>{workspace} shows up in your workspace switcher next to the ones you're already in.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive" role="alert" className="enter-in">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <Button
          size="lg"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            setError(undefined)
            try {
              await acceptInvite({ data: { token } })
              await onJoined()
            } catch (err) {
              setError(errorMessage(err))
            } finally {
              setBusy(false)
            }
          }}
        >
          {busy && <Spinner data-icon="inline-start" />}
          Join {workspace}
        </Button>
      </CardContent>
    </Card>
  )
}

function NewAccountForm({ token, onJoined }: { token: string; onJoined: () => Promise<void> }) {
  const [username, setUsername] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [confirm, setConfirm] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [errors, setErrors] = React.useState<{ username?: string; password?: string; confirm?: string; form?: string }>({})

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    const u = username.trim()
    if (u.length < 3) next.username = "Use at least 3 characters."
    else if (u.length > 32) next.username = "Use at most 32 characters."
    else if (!/^[a-zA-Z0-9_.]+$/.test(u)) next.username = "Use letters, numbers, dots and underscores only."
    if (password.length < MIN_PASSWORD) next.password = `Use at least ${MIN_PASSWORD} characters.`
    if (!next.password && confirm !== password) next.confirm = "The passwords don't match."
    setErrors(next)
    if (Object.keys(next).length) {
      document.getElementById(next.username ? "new-username" : next.password ? "new-password" : "confirm-password")?.focus()
      return
    }
    setBusy(true)
    try {
      await acceptInviteNewAccount({ data: { token, username: u, password } })
      await onJoined()
    } catch (err) {
      const msg = errorMessage(err)
      if (/username/i.test(msg)) setErrors({ username: msg.replace(/^Username: /, "") })
      else if (/^Password: /.test(msg)) setErrors({ password: msg.replace(/^Password: /, "") })
      else setErrors({ form: msg })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardContent>
        <form noValidate onSubmit={submit}>
          <FieldGroup>
            {errors.form && (
              <Alert variant="destructive" role="alert" className="enter-in">
                <AlertDescription>{errors.form}</AlertDescription>
              </Alert>
            )}
            <Field data-invalid={!!errors.username}>
              <FieldLabel htmlFor="new-username">Username</FieldLabel>
              <Input
                id="new-username"
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                autoFocus
                maxLength={32}
                value={username}
                aria-invalid={!!errors.username}
                aria-describedby="new-username-hint"
                onChange={(e) => setUsername(e.target.value)}
              />
              {errors.username ? (
                <FieldError id="new-username-hint">{errors.username}</FieldError>
              ) : (
                <FieldDescription id="new-username-hint">You log in with this. Letters, numbers, dots and underscores.</FieldDescription>
              )}
            </Field>
            <Field data-invalid={!!errors.password}>
              <FieldLabel htmlFor="new-password">Password</FieldLabel>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                value={password}
                aria-invalid={!!errors.password}
                aria-describedby="new-password-hint"
                onChange={(e) => setPassword(e.target.value)}
              />
              {errors.password ? (
                <FieldError id="new-password-hint">{errors.password}</FieldError>
              ) : (
                <FieldDescription id="new-password-hint">At least {MIN_PASSWORD} characters. You can add a passkey after.</FieldDescription>
              )}
            </Field>
            <Field data-invalid={!!errors.confirm}>
              <FieldLabel htmlFor="confirm-password">Confirm password</FieldLabel>
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
            <Button type="submit" size="lg" disabled={busy}>
              {busy && <Spinner data-icon="inline-start" />}
              Create account and join
            </Button>
            <p className="text-center text-caption text-muted-foreground">
              Already have an account?{" "}
              <Link to="/login" search={{ next: `/invite/${token}` }} className="font-medium text-foreground underline underline-offset-4">
                Log in first
              </Link>
            </p>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  )
}
