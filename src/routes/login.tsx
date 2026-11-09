import * as React from "react"
import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { z } from "zod"

import { RecallMark } from "@/components/app-sidebar"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { getSession, login } from "@/lib/fns"
import { errorMessage } from "@/lib/format"

export const Route = createFileRoute("/login")({
  validateSearch: z.object({ next: z.string().optional() }),
  beforeLoad: async () => {
    const session = await getSession()
    if (session.authed) throw redirect({ to: "/" })
    return { configured: session.configured }
  },
  head: () => ({ meta: [{ title: "Log in · Recall" }] }),
  component: LoginPage,
})

function LoginPage() {
  const { configured } = Route.useRouteContext()
  const { next } = Route.useSearch()
  const router = useRouter()
  const [password, setPassword] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  const input = React.useRef<HTMLInputElement>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (password === "") {
      setError("Enter the admin password.")
      input.current?.focus()
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      const res = await login({ data: { password } })
      if (res.ok) {
        // Only same-site paths, never an absolute URL from the query string.
        const target = next?.startsWith("/") && !next.startsWith("//") ? next : "/"
        await router.invalidate()
        await router.navigate({ href: target })
        return
      }
      if (res.reason === "locked") setError(`Too many wrong tries. Try again in ${res.retryInMin} min.`)
      else if (res.reason === "not_configured") setError("The server has no admin password set yet.")
      else setError(res.left > 0 ? `Wrong password. ${res.left} ${res.left === 1 ? "try" : "tries"} left before a 15 minute lock.` : "Wrong password.")
      input.current?.select()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const invalid = error !== undefined

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
          <div className="flex flex-col gap-1.5">
            <h1 className="font-heading text-title">Log in to Recall</h1>
            <p className="text-sm text-pretty text-muted-foreground">One admin, one password.</p>
          </div>
        </div>

        {!configured && (
          <Alert variant="destructive">
            <AlertTitle>Login isn't set up on this server</AlertTitle>
            <AlertDescription>
              Set <code>ADMIN_PASSWORD_HASH</code> (from <code>pnpm cli hash-password</code>) and a 32+ character <code>SESSION_SECRET</code> in the
              server's environment, then restart it.
            </AlertDescription>
          </Alert>
        )}

        <form noValidate onSubmit={submit}>
          <Card>
          <CardContent>
          <FieldGroup>
            <Field data-invalid={invalid}>
              <FieldLabel htmlFor="password">Admin password</FieldLabel>
              <Input
                ref={input}
                id="password"
                name="password"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={password}
                aria-invalid={invalid}
                aria-describedby={invalid ? "password-error" : "password-hint"}
                onChange={(e) => setPassword(e.target.value)}
              />
              {invalid ? (
                <FieldError id="password-error">{error}</FieldError>
              ) : (
                <FieldDescription id="password-hint">The one hashed into ADMIN_PASSWORD_HASH.</FieldDescription>
              )}
            </Field>
            <Button type="submit" size="lg" disabled={busy || !configured}>
              {busy && <Spinner data-icon="inline-start" />}
              Log in
            </Button>
          </FieldGroup>
          </CardContent>
          </Card>
        </form>
      </main>
    </div>
  )
}
