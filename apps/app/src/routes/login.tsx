import * as React from "react"
import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { FingerprintIcon } from "lucide-react"
import { z } from "zod"

import { RecallMark } from "@/components/app-sidebar"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Field, FieldError, FieldGroup, FieldLabel, FieldSeparator } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { authClient, authErrorMessage, passkeyCancelled } from "@/lib/auth-client"
import { getSession } from "@/lib/fns"
import { errorMessage } from "@/lib/format"
import { inOAuthLogin, restoreSignedOAuthQuery } from "@/lib/oauth-query"

export const Route = createFileRoute("/login")({
  validateSearch: z.object({ next: z.string().optional(), oauth: z.unknown().optional() }),
  beforeLoad: async () => {
    const session = await getSession()
    if (session.viewer) throw redirect({ to: "/" })
    return { configured: session.configured, hasUsers: session.hasUsers }
  },
  head: () => ({ meta: [{ title: "Log in · Recall" }] }),
  component: LoginPage,
})

// Only same-site paths, never an absolute URL from the query string.
export function safeNext(next: string | undefined) {
  return next?.startsWith("/") && !next.startsWith("//") ? next : "/"
}

function LoginPage() {
  const { configured, hasUsers } = Route.useRouteContext()
  const { next } = Route.useSearch()
  const router = useRouter()
  const [username, setUsername] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [busy, setBusy] = React.useState<"password" | "passkey" | null>(null)
  const [error, setError] = React.useState<{ field: "username" | "password" | "form"; text: string }>()
  const usernameInput = React.useRef<HTMLInputElement>(null)
  const passwordInput = React.useRef<HTMLInputElement>(null)
  const ready = configured && hasUsers

  // During an OAuth sign-in (claude.ai connecting), the server answers the
  // login with the next step: the consent page or the app's redirect URL.
  const done = async (data?: unknown) => {
    const oauthNext = (data as { redirect?: boolean; url?: string } | null | undefined)?.url
    if ((data as { redirect?: boolean } | null | undefined)?.redirect && oauthNext) {
      window.location.assign(oauthNext)
      return
    }
    await router.invalidate()
    await router.navigate({ href: safeNext(next) })
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim()) {
      setError({ field: "username", text: "Enter your username." })
      usernameInput.current?.focus()
      return
    }
    if (password === "") {
      setError({ field: "password", text: "Enter your password." })
      passwordInput.current?.focus()
      return
    }
    setBusy("password")
    setError(undefined)
    try {
      if (inOAuthLogin()) restoreSignedOAuthQuery()
      const res = await authClient.signIn.username({ username: username.trim(), password })
      if (res.error) {
        setError({ field: "password", text: authErrorMessage(res.error, "Couldn't sign in.") })
        passwordInput.current?.select()
        return
      }
      await done(res.data)
    } catch (err) {
      setError({ field: "form", text: errorMessage(err) })
    } finally {
      setBusy(null)
    }
  }

  const withPasskey = async () => {
    setBusy("passkey")
    setError(undefined)
    try {
      if (inOAuthLogin()) restoreSignedOAuthQuery()
      const res = await authClient.signIn.passkey()
      if (res?.error) {
        if (!passkeyCancelled(res.error)) setError({ field: "form", text: authErrorMessage(res.error, "That passkey didn't work. Try again or use your password.") })
        return
      }
      await done(res?.data)
    } catch (err) {
      if (!passkeyCancelled({ message: errorMessage(err) })) setError({ field: "form", text: errorMessage(err) })
    } finally {
      setBusy(null)
    }
  }

  const fieldError = (f: "username" | "password") => (error?.field === f ? error.text : undefined)

  return (
    <div className="relative flex min-h-svh flex-col items-center justify-center bg-background px-4 py-12">
      <div className="absolute end-4 top-4">
        <ThemeToggle />
      </div>
      <main className="stagger-in relative flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col items-center gap-4 text-center">
          <span
            aria-hidden="true"
            className="flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-[0_1px_2px_oklch(0_0_0/0.12),inset_0_1px_0_oklch(1_0_0/0.15)]"
          >
            <RecallMark className="size-7" />
          </span>
          <h1 className="font-heading text-title">Log in to Recall</h1>
        </div>

        {!configured && (
          <Alert variant="destructive">
            <AlertTitle>Login isn't set up on this server</AlertTitle>
            <AlertDescription>
              Set a 32+ character <code>SESSION_SECRET</code> and <code>RECALL_URL</code> in the server's environment, then restart it.
            </AlertDescription>
          </Alert>
        )}
        {configured && !hasUsers && (
          <Alert>
            <AlertTitle>No accounts yet</AlertTitle>
            <AlertDescription>
              Create the first owner on the server with <code>recall create-user &lt;username&gt; --role owner</code>, or set{" "}
              <code>ADMIN_PASSWORD_HASH</code> and restart to create <code>admin</code>.
            </AlertDescription>
          </Alert>
        )}

        <Card>
          <CardContent>
            <form noValidate onSubmit={submit}>
              <FieldGroup>
                {error?.field === "form" && (
                  <Alert variant="destructive" role="alert" className="enter-in">
                    <AlertDescription>{error.text}</AlertDescription>
                  </Alert>
                )}
                <Field data-invalid={!!fieldError("username")}>
                  <FieldLabel htmlFor="username">Username</FieldLabel>
                  <Input
                    ref={usernameInput}
                    id="username"
                    name="username"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    autoFocus
                    value={username}
                    aria-invalid={!!fieldError("username")}
                    aria-describedby={fieldError("username") ? "username-error" : undefined}
                    onChange={(e) => setUsername(e.target.value)}
                  />
                  {fieldError("username") && <FieldError id="username-error">{fieldError("username")}</FieldError>}
                </Field>
                <Field data-invalid={!!fieldError("password")}>
                  <FieldLabel htmlFor="password">Password</FieldLabel>
                  <Input
                    ref={passwordInput}
                    id="password"
                    name="password"
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    aria-invalid={!!fieldError("password")}
                    aria-describedby={fieldError("password") ? "password-error" : undefined}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                  {fieldError("password") && <FieldError id="password-error">{fieldError("password")}</FieldError>}
                </Field>
                <Button type="submit" disabled={busy !== null || !ready}>
                  {busy === "password" && <Spinner data-icon="inline-start" />}
                  Log in
                </Button>
                <FieldSeparator>or</FieldSeparator>
                <Button type="button" variant="outline" disabled={busy !== null || !ready} onClick={() => void withPasskey()}>
                  {busy === "passkey" ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
                  Log in with a passkey
                </Button>
              </FieldGroup>
            </form>
          </CardContent>
        </Card>
        <p className="text-center text-caption text-pretty text-muted-foreground">New here? Ask for an invite link.</p>
      </main>
    </div>
  )
}
