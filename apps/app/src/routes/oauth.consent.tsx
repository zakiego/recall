// Where an OAuth client like claude.ai sends the user to approve access. Better
// Auth redirects here with a signed copy of the authorization request in the
// query string; approving posts it back to /api/auth/oauth2/consent, which
// answers with the client's redirect URL (carrying the code).
import * as React from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { PlugIcon } from "lucide-react"

import { RecallMark } from "@/components/app-sidebar"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldContent, FieldLabel, FieldTitle } from "@/components/ui/field"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Spinner } from "@/components/ui/spinner"
import { authClient } from "@/lib/auth-client"
import { errorMessage } from "@/lib/format"
import { switchWorkspace } from "@/lib/fns"
import { getConsentRequest, type ConsentRequest } from "@/lib/oauth-fns"
import { clearSignedOAuthQuery, restoreSignedOAuthQuery, signedOAuthQuery } from "@/lib/oauth-query"

export const Route = createFileRoute("/oauth/consent")({
  head: () => ({ meta: [{ title: "Connect an app · Recall" }] }),
  component: ConsentPage,
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

function ConsentPage() {
  const [request, setRequest] = React.useState<ConsentRequest>()
  const [loadError, setLoadError] = React.useState<string>()

  // The signature covers the exact query string, so use the copy kept in a
  // cookie by /app/oauth/approve (see lib/oauth-query.ts).
  React.useEffect(() => {
    getConsentRequest({ data: { query: signedOAuthQuery() } })
      .then(setRequest)
      .catch((err) => setLoadError(errorMessage(err)))
  }, [])

  if (loadError) {
    return (
      <Frame>
        <Alert variant="destructive">
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      </Frame>
    )
  }
  if (!request) {
    return (
      <Frame>
        <div className="flex justify-center py-8 text-muted-foreground">
          <Spinner />
        </div>
      </Frame>
    )
  }
  if (!request.ok) {
    return (
      <Frame>
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <PlugIcon />
            </EmptyMedia>
            <EmptyTitle>Can't connect this app</EmptyTitle>
            <EmptyDescription>{request.problem}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" render={<Link to={request.signedIn ? "/" : "/login"} search={{}} />}>
              {request.signedIn ? "Go to Recall" : "Log in"}
            </Button>
          </EmptyContent>
        </Empty>
      </Frame>
    )
  }
  return (
    <Frame>
      <Approve request={request} />
    </Frame>
  )
}

function Approve({ request }: { request: Extract<ConsentRequest, { ok: true }> }) {
  const [workspaceId, setWorkspaceId] = React.useState(request.activeWorkspaceId ?? request.workspaces[0].id)
  const [busy, setBusy] = React.useState<"allow" | "deny" | null>(null)
  const [error, setError] = React.useState<string>()
  const workspaceName = request.workspaces.find((w) => w.id === workspaceId)?.name ?? "your workspace"

  const answer = async (accept: boolean) => {
    setBusy(accept ? "allow" : "deny")
    setError(undefined)
    try {
      // The token is tied to the session's active workspace at this moment.
      if (accept && workspaceId !== request.activeWorkspaceId) await switchWorkspace({ data: { id: workspaceId } })
      restoreSignedOAuthQuery()
      const res = await authClient.oauth2.consent({ accept })
      if (res.error) throw new Error(res.error.message || "Couldn't finish connecting.")
      const url = (res.data as { url?: string } | null)?.url
      if (!url) throw new Error("Recall didn't say where to go next. Start connecting again from the app.")
      clearSignedOAuthQuery()
      window.location.assign(url)
    } catch (err) {
      setError(errorMessage(err))
      setBusy(null)
    }
  }

  return (
    <>
      <div className="flex flex-col gap-1.5 text-center">
        <h1 className="font-heading text-title text-balance">Connect {request.client.name}</h1>
        <p className="text-sm text-pretty text-muted-foreground">{request.client.name} can search, add and change your memories.</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Allow access to {workspaceName}?</CardTitle>
          <CardDescription>
            Signed in as {request.username}
            {request.client.redirectHost && (
              <>
                , then back to <span className="font-medium text-foreground">{request.client.redirectHost}</span>
              </>
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {request.workspaces.length > 1 && (
            <RadioGroup aria-label="Workspace" value={workspaceId} onValueChange={(v) => setWorkspaceId(String(v))} className="flex flex-col gap-2">
              {request.workspaces.map((w) => (
                <FieldLabel key={w.id} htmlFor={`ws-${w.id}`} className="bg-card">
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldTitle id={`ws-${w.id}-title`}>{w.name}</FieldTitle>
                    </FieldContent>
                    <RadioGroupItem value={w.id} id={`ws-${w.id}`} aria-labelledby={`ws-${w.id}-title`} />
                  </Field>
                </FieldLabel>
              ))}
            </RadioGroup>
          )}
          {error && (
            <Alert variant="destructive" role="alert" className="enter-in">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-col gap-2">
            <Button size="lg" disabled={busy !== null} onClick={() => void answer(true)}>
              {busy === "allow" && <Spinner data-icon="inline-start" />}
              Allow
            </Button>
            <Button size="lg" variant="ghost" disabled={busy !== null} onClick={() => void answer(false)}>
              {busy === "deny" && <Spinner data-icon="inline-start" />}
              Cancel
            </Button>
          </div>
        </CardContent>
      </Card>
    </>
  )
}
