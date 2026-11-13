import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, CircleCheckIcon, EllipsisIcon, FileTextIcon, InboxIcon, MailIcon, PlugIcon, RotateCwIcon, SearchIcon, UnplugIcon } from "lucide-react"
import { z } from "zod"

import { AppConnectorCard } from "@/components/app-connector-card"
import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { StatusBadge } from "@/components/status-badge"
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
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button, buttonVariants } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Separator } from "@/components/ui/separator"
import { PagePending } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { useContainer } from "@/hooks/use-container"
import { APP_BASE } from "@/lib/app-path"
import { disconnectGmail, getConnectors, importGmailMessages, searchGmailMessages } from "@/lib/connector-fns"
import { retryDocument } from "@/lib/fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"

const OUTCOMES = ["connected", "denied", "failed", "expired", "not_configured"] as const

export const Route = createFileRoute("/_app/connectors")({
  // ?gmail= is where the OAuth callback says how connecting went. The search
  // query stays out of the URL on purpose: mail searches don't belong in
  // browser history or proxy logs.
  validateSearch: z.object({ gmail: z.enum(OUTCOMES).optional() }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c }),
  loader: ({ deps, context }) => getConnectors({ data: { container: deps.c ?? context.defaultContainer } }),
  pendingComponent: () => (
    <PagePending label="Loading connectors…">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-56 w-full rounded-xl" />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Connectors · Recall" }] }),
  component: ConnectorsPage,
})

type Data = Awaited<ReturnType<typeof getConnectors>>
type Result = Awaited<ReturnType<typeof searchGmailMessages>>["messages"][number]

const POLL_MS = 2_000
const EXAMPLES = ["from:github", "label:bills", "after:2026/11/01", "has:attachment invoice"]

const OUTCOME_TOAST: Record<(typeof OUTCOMES)[number], { title: string; description?: string; type: "success" | "error" | "info" }> = {
  connected: { title: "Gmail connected", description: "Search below and pick what Recall should learn.", type: "success" },
  denied: { title: "Gmail wasn't connected", description: "Google said access wasn't granted. Connect again and allow reading mail.", type: "info" },
  failed: { title: "Couldn't connect Gmail", description: "The sign-in didn't finish. Try again from this page.", type: "error" },
  expired: { title: "That sign-in took too long", description: "Connect again; the link from Google is good for 10 minutes.", type: "info" },
  not_configured: { title: "Gmail isn't set up on this server", type: "error" },
}

// These go to a server route (a full page load to Google and back), so they
// are plain links styled as buttons, with the same press feedback as Button.
const linkButton = (opts?: Parameters<typeof buttonVariants>[0]) => cn(buttonVariants(opts), "active:scale-[0.96]")
const startUrl = (container: string) => `${APP_BASE}/connectors/google/start?${new URLSearchParams({ c: container })}`

function ConnectorsPage() {
  const container = useContainer()
  const data = Route.useLoaderData()
  const router = useRouter()
  const { gmail: outcome } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const pending = data.imports.some((d) => d.status === "queued" || d.status === "processing")
  const refresh = React.useCallback(() => void router.invalidate(), [router])

  React.useEffect(() => {
    if (!outcome) return
    toast.add(OUTCOME_TOAST[outcome])
    void navigate({ search: (prev) => ({ ...prev, gmail: undefined }), replace: true })
  }, [outcome, navigate])

  // Poll only while an import is in flight, and only when the tab is visible.
  React.useEffect(() => {
    if (!pending) return
    const t = setInterval(() => {
      if (document.visibilityState === "visible") refresh()
    }, POLL_MS)
    return () => clearInterval(t)
  }, [pending, refresh])

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Connectors"
        description={
          <>
            Bring email into <span className="font-mono wrap-anywhere text-foreground">{container}</span> by hand, and let Oslo and Finansial sync on their
            own every hour.
          </>
        }
      />
      {data.configured ? <GmailCard data={data} container={container} onChange={refresh} /> : <NotConfigured data={data} />}
      {data.apps.map((app) => (
        <AppConnectorCard
          key={`${app.provider}:${container}`}
          app={app}
          container={container}
          canManage={data.canManage}
          secretConfigured={data.appSecretConfigured}
          onChange={refresh}
        />
      ))}
      {data.canManage && data.connections.length > 0 && <WorkspaceConnections data={data} />}
      {data.configured && <RecentImports key={container} imports={data.imports} container={container} onChange={refresh} />}
    </div>
  )
}

function NotConfigured({ data }: { data: Data }) {
  return (
    <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <MailIcon />
        </EmptyMedia>
        <EmptyTitle>Gmail isn't set up on this server</EmptyTitle>
        <EmptyDescription>
          {data.canManage
            ? "Whoever runs Recall needs a Google OAuth client and these keys in the server's environment, then a restart."
            : "Ask a workspace owner or admin to set it up. Everything else in Recall works as usual."}
        </EmptyDescription>
      </EmptyHeader>
      {data.missing.length > 0 && (
        <EmptyContent>
          <ul className="flex flex-wrap justify-center gap-1.5" aria-label="Missing environment keys">
            {data.missing.map((k) => (
              <li key={k}>
                <Badge variant="outline" className="font-mono">
                  {k}
                </Badge>
              </li>
            ))}
          </ul>
          <a href="/docs/gmail" className={buttonVariants({ variant: "link", size: "caption" })}>
            Setup steps in the docs
          </a>
        </EmptyContent>
      )}
    </Empty>
  )
}

function GmailCard({ data, container, onChange }: { data: Data; container: string; onChange: () => void }) {
  const [disconnecting, setDisconnecting] = React.useState(false)
  const [busy, setBusy] = React.useState(false)
  const gmail = data.gmail
  const address = <span className="font-mono wrap-anywhere text-foreground">{gmail?.email ?? "your Google account"}</span>

  const disconnect = async () => {
    setBusy(true)
    try {
      const res = await disconnectGmail()
      setDisconnecting(false)
      toast.add(
        res.revoked
          ? { title: "Gmail disconnected", description: "Access revoked at Google. Imported messages stay.", type: "success" }
          : {
              title: "Disconnected here",
              description: "Google didn't confirm the revoke. Remove Recall at myaccount.google.com/permissions to be sure.",
              type: "info",
            },
      )
      onChange()
    } catch (err) {
      toast.add({ title: "That didn't work", description: errorMessage(err), type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Gmail</CardTitle>
        <CardDescription className="min-w-0">
          {!gmail ? (
            "Read-only. Recall reads a message only when you import it."
          ) : gmail.status === "reauth" ? (
            <>Access ended for {address}</>
          ) : (
            <>
              Connected as {address}
              <span className="max-sm:hidden"> · {ago(gmail.connectedAt)}</span>
            </>
          )}
        </CardDescription>
        <CardAction>
          {gmail ? (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label="Gmail connection actions" disabled={busy} />}>
                <EllipsisIcon aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-52">
                <DropdownMenuGroup>
                  <DropdownMenuItem render={<a href={startUrl(container)} />}>
                    <RotateCwIcon />
                    Connect again
                  </DropdownMenuItem>
                  <DropdownMenuItem variant="destructive" onSelect={() => setDisconnecting(true)}>
                    <UnplugIcon />
                    Disconnect
                  </DropdownMenuItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <a href={startUrl(container)} className={linkButton()}>
              <PlugIcon data-icon="inline-start" />
              Connect Gmail
            </a>
          )}
        </CardAction>
      </CardHeader>

      {gmail?.status === "reauth" && (
        <CardContent>
          <Alert variant="warning">
            <CircleAlertIcon />
            <AlertTitle>Gmail access ended</AlertTitle>
            <AlertDescription>
              <p>Google revoked or expired Recall's access. Connect again to keep importing.</p>
              <a href={startUrl(container)} className={cn(linkButton({ size: "sm", variant: "outline" }), "mt-2")}>
                Connect again
              </a>
            </AlertDescription>
          </Alert>
        </CardContent>
      )}

      {gmail?.status === "active" && (
        <CardContent className="flex flex-col gap-4">
          <Separator />
          <GmailSearch container={container} maxImport={data.maxImport} onImported={onChange} />
        </CardContent>
      )}

      <AlertDialog open={disconnecting} onOpenChange={setDisconnecting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Disconnect Gmail?</AlertDialogTitle>
            <AlertDialogDescription>
              Recall revokes its access at Google and deletes the stored tokens. Messages you already imported stay, and imports still queued will fail.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="danger" disabled={busy} data-agent-confirm="Disconnects Gmail from this workspace" onClick={() => void disconnect()}>
              {busy && <Spinner data-icon="inline-start" />}
              Disconnect
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}

type SearchState =
  | { kind: "idle" }
  | { kind: "loading"; q: string }
  | { kind: "error"; q: string; message: string }
  | { kind: "done"; q: string; messages: Result[]; nextPageToken: string | null; estimate: number }

// A row can be ticked when it isn't in the container yet, or its import failed.
const importable = (m: Result) => !m.imported || m.importFailed

function GmailSearch({ container, maxImport, onImported }: { container: string; maxImport: number; onImported: () => void }) {
  const [q, setQ] = React.useState("")
  const [state, setState] = React.useState<SearchState>({ kind: "idle" })
  const [selected, setSelected] = React.useState<Set<string>>(new Set())
  const [moreBusy, setMoreBusy] = React.useState(false)
  const [importing, setImporting] = React.useState(false)
  const inputId = React.useId()
  const hintId = React.useId()
  // Each search gets a number; a response for an older one (or for the
  // container before a switch) is dropped instead of landing on top.
  const seq = React.useRef(0)

  // Results are marked imported per container, so a container switch starts over.
  React.useEffect(() => {
    seq.current += 1
    setState({ kind: "idle" })
    setSelected(new Set())
  }, [container])

  const run = async (query: string) => {
    const mine = ++seq.current
    setState({ kind: "loading", q: query })
    setSelected(new Set())
    try {
      const r = await searchGmailMessages({ data: { container, q: query } })
      if (mine === seq.current) setState({ kind: "done", q: query, ...r })
    } catch (err) {
      if (mine === seq.current) setState({ kind: "error", q: query, message: errorMessage(err) })
    }
  }

  const loadMore = async () => {
    if (state.kind !== "done" || !state.nextPageToken) return
    const mine = seq.current
    setMoreBusy(true)
    try {
      const r = await searchGmailMessages({ data: { container, q: state.q, pageToken: state.nextPageToken } })
      if (mine !== seq.current) return
      setState((s) => (s.kind === "done" ? { ...s, messages: [...s.messages, ...r.messages], nextPageToken: r.nextPageToken } : s))
    } catch (err) {
      toast.add({ title: "Couldn't load more", description: errorMessage(err), type: "error" })
    } finally {
      setMoreBusy(false)
    }
  }

  const importSelected = async () => {
    if (state.kind !== "done" || selected.size === 0) return
    const picked = state.messages.filter((m) => selected.has(m.id))
    setImporting(true)
    try {
      const r = await importGmailMessages({ data: { container, messages: picked.map((m) => ({ id: m.id, subject: m.subject || null })) } })
      const done = new Set(picked.map((m) => m.id))
      setState((s) => (s.kind === "done" ? { ...s, messages: s.messages.map((m) => (done.has(m.id) ? { ...m, imported: true, importFailed: false } : m)) } : s))
      setSelected(new Set())
      toast.add({
        title: r.queued ? `Importing ${plural(r.queued, "message")}` : "Already imported",
        description: r.queued
          ? `PDF attachments come along as their own sources.${r.duplicates ? ` ${plural(r.duplicates, "message was", "messages were")} already here.` : ""}`
          : `Those messages are already in ${container}.`,
        type: "success",
      })
      onImported()
    } catch (err) {
      toast.add({ title: "Import didn't start", description: errorMessage(err), type: "error" })
    } finally {
      setImporting(false)
    }
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    void run(q.trim())
  }

  return (
    <div className="flex flex-col gap-4">
      <form onSubmit={submit} role="search">
        <Field>
          <FieldLabel htmlFor={inputId}>Search your mail</FieldLabel>
          <div className="flex gap-2">
            <InputGroup className="min-w-0 flex-1">
              <InputGroupAddon>
                <SearchIcon aria-hidden="true" />
              </InputGroupAddon>
              <InputGroupInput
                id={inputId}
                aria-describedby={hintId}
                type="search"
                enterKeyHint="search"
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                className="font-mono"
                placeholder="from:bank label:receipts after:2026/10/01"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </InputGroup>
            <Button type="submit" variant="outline" className="min-w-22" disabled={state.kind === "loading"}>
              {state.kind === "loading" && <Spinner data-icon="inline-start" />}
              Search
            </Button>
          </div>
          <FieldDescription className="flex flex-wrap items-center gap-1.5">
            <span id={hintId}>Gmail search works here, for example from:, label:, after: and has:attachment. Try</span>
            {EXAMPLES.map((ex) => (
              <Button
                key={ex}
                type="button"
                variant="secondary"
                size="xs"
                className="font-mono pointer-coarse:h-8"
                disabled={state.kind === "loading"}
                onClick={() => {
                  setQ(ex)
                  void run(ex)
                }}
              >
                {ex}
              </Button>
            ))}
          </FieldDescription>
        </Field>
      </form>

      <Results
        state={state}
        container={container}
        selected={selected}
        setSelected={setSelected}
        maxImport={maxImport}
        importing={importing}
        moreBusy={moreBusy}
        onImport={() => void importSelected()}
        onMore={() => void loadMore()}
        onRetry={() => state.kind === "error" && void run(state.q)}
      />
    </div>
  )
}

function Results({
  state,
  container,
  selected,
  setSelected,
  maxImport,
  importing,
  moreBusy,
  onImport,
  onMore,
  onRetry,
}: {
  state: SearchState
  container: string
  selected: Set<string>
  setSelected: (s: Set<string>) => void
  maxImport: number
  importing: boolean
  moreBusy: boolean
  onImport: () => void
  onMore: () => void
  onRetry: () => void
}) {
  const limitId = React.useId()
  if (state.kind === "idle") {
    return <p className="text-caption text-muted-foreground">Search to see messages, tick the ones worth remembering, then import them.</p>
  }
  if (state.kind === "loading") {
    return (
      <div className="flex flex-col gap-3" role="status" aria-busy="true">
        <span className="sr-only">Searching Gmail</span>
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="flex gap-3">
            <Skeleton className="mt-0.5 size-4 rounded-[4px]" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-3 w-1/3" />
              <Skeleton className="h-3 w-full" />
            </div>
          </div>
        ))}
      </div>
    )
  }
  if (state.kind === "error") {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon />
        <AlertTitle>Search didn't work</AlertTitle>
        <AlertDescription>{state.message}</AlertDescription>
        <AlertAction>
          <Button size="sm" variant="outline" onClick={onRetry}>
            Retry
          </Button>
        </AlertAction>
      </Alert>
    )
  }
  if (state.messages.length === 0) {
    return (
      <Empty className="rounded-lg border border-dashed py-8">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <InboxIcon />
          </EmptyMedia>
          <EmptyTitle>No messages match</EmptyTitle>
          <EmptyDescription>
            Gmail found nothing for <span className="font-mono wrap-anywhere text-foreground">{state.q || "an empty search"}</span>. Loosen a filter or check
            the spelling of a label.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  // Select all takes the first maxImport, so it never makes a selection that
  // can't be imported.
  const open = state.messages.filter(importable)
  const pickable = open.slice(0, maxImport)
  const allOn = pickable.length > 0 && pickable.every((m) => selected.has(m.id))
  const someOn = state.messages.some((m) => selected.has(m.id))
  const tooMany = selected.size > maxImport
  const toggle = (id: string, on: boolean) => {
    const next = new Set(selected)
    if (on) next.add(id)
    else next.delete(id)
    setSelected(next)
  }

  return (
    <section aria-label="Gmail results" className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <div className="flex items-center gap-2.5 text-sm">
          <Checkbox
            aria-label={open.length > maxImport ? `Select the first ${maxImport} messages` : "Select all messages"}
            checked={allOn}
            indeterminate={someOn && !allOn}
            disabled={pickable.length === 0}
            onCheckedChange={(on) => setSelected(on ? new Set(pickable.map((m) => m.id)) : new Set())}
          />
          <span className="tabular-nums" aria-live="polite">
            {selected.size ? `${selected.size} selected` : `${plural(state.messages.length, "message")}${state.nextPageToken ? ", more below" : ""}`}
          </span>
        </div>
        <Button
          size="sm"
          disabled={selected.size === 0 || tooMany || importing}
          onClick={onImport}
          className="max-w-full"
          aria-describedby={tooMany ? limitId : undefined}
        >
          {importing && <Spinner data-icon="inline-start" />}
          <span className="truncate">
            Import to <span className="font-mono">{container}</span>
          </span>
        </Button>
      </div>
      {tooMany && (
        <p id={limitId} className="text-caption text-destructive-fg">
          Import up to {maxImport} at a time. Untick a few.
        </p>
      )}
      <ul className="flex flex-col overflow-hidden rounded-lg border transition-opacity duration-150 ease-out starting:opacity-0">
        {state.messages.map((m) => (
          <ResultRow key={m.id} m={m} checked={selected.has(m.id)} onToggle={(on) => toggle(m.id, on)} />
        ))}
      </ul>
      {state.nextPageToken && (
        <Button variant="ghost" size="sm" className="self-center" disabled={moreBusy} onClick={onMore}>
          {moreBusy && <Spinner data-icon="inline-start" />}
          Load more
        </Button>
      )}
    </section>
  )
}

function ResultRow({ m, checked, onToggle }: { m: Result; checked: boolean; onToggle: (on: boolean) => void }) {
  const subject = m.subject || "(no subject)"
  const done = m.imported && !m.importFailed
  const date = m.date ? (
    <time className="font-mono tabular-nums" dateTime={new Date(m.date).toISOString()} title={fullDate(m.date)}>
      {shortDate(m.date)}
    </time>
  ) : null
  return (
    <li className="border-b last:border-b-0 data-imported:bg-muted/40" data-imported={done || undefined}>
      <label className="flex cursor-pointer items-start gap-3 px-3 py-2.5 transition-colors hover:bg-muted/40 has-disabled:cursor-default has-disabled:hover:bg-transparent">
        <Checkbox className="mt-0.5" checked={checked || done} disabled={done} onCheckedChange={(on) => onToggle(on === true)} aria-label={subject} />
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex min-w-0 items-start gap-2">
            <span className="line-clamp-2 min-w-0 flex-1 text-sm font-medium break-words">{subject}</span>
            {done && (
              <Badge variant="success" className="shrink-0 transition-[opacity,scale] duration-150 ease-emphasized starting:scale-96 starting:opacity-0">
                <CircleCheckIcon aria-hidden="true" />
                Imported
              </Badge>
            )}
            {m.importFailed && (
              <Badge variant="destructive" className="shrink-0">
                <CircleAlertIcon aria-hidden="true" />
                Failed
              </Badge>
            )}
            {date && <span className="shrink-0 text-caption text-muted-foreground max-sm:hidden">{date}</span>}
          </span>
          <span className="text-caption text-muted-foreground">
            {/* Addresses break anywhere; names and the date only at spaces. */}
            <span className="wrap-anywhere">{m.from || "Unknown sender"}</span>
            {date && <span className="whitespace-nowrap sm:hidden"> · {date}</span>}
          </span>
          {m.snippet && <span className="line-clamp-2 text-caption break-words text-muted-foreground">{m.snippet}</span>}
          {m.importFailed && <span className="text-caption text-destructive-fg">The last import failed. Tick it and import again to retry.</span>}
        </span>
      </label>
    </li>
  )
}

function shortDate(ms: number) {
  const d = new Date(ms)
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { year: "numeric", month: "short", day: "numeric" })
}

function WorkspaceConnections({ data }: { data: Data }) {
  return (
    <section aria-labelledby="connections" className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id="connections" className="font-heading text-heading">
          Connected in this workspace
        </h2>
        <p className="text-caption text-muted-foreground">You can see who connected. Only the person who connected can search or import their mail.</p>
      </div>
      <ul className="flex flex-col overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
        {data.connections.map((c) => (
          <li key={c.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-4 py-3 last:border-b-0">
            <MailIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
            <span className="flex min-w-0 flex-1 items-center gap-2">
              <span className="min-w-0 text-sm font-medium break-words">{c.username}</span>
              {c.mine && (
                <Badge variant="outline" className="shrink-0">
                  You
                </Badge>
              )}
            </span>
            {c.status === "reauth" ? <Badge variant="warning">Needs reconnect</Badge> : <Badge variant="success">Connected</Badge>}
            <time className="font-mono text-caption text-muted-foreground tabular-nums" dateTime={new Date(c.createdAt).toISOString()} title={fullDate(c.createdAt)}>
              {ago(c.createdAt)}
            </time>
          </li>
        ))}
      </ul>
    </section>
  )
}

function RecentImports({ imports, container, onChange }: { imports: Data["imports"]; container: string; onChange: () => void }) {
  const inFlight = imports.filter((d) => d.status === "queued" || d.status === "processing").length
  // Rows rendered with the page are just there; only rows that arrive while
  // the page is open slide in, so a visit doesn't replay them.
  const [live, setLive] = React.useState(false)
  React.useEffect(() => setLive(true), [])
  const [retrying, setRetrying] = React.useState<string | null>(null)

  const retry = async (id: string) => {
    setRetrying(id)
    try {
      await retryDocument({ data: { id } })
      toast.add({ title: "Queued again", type: "success" })
      onChange()
    } catch (err) {
      toast.add({ title: "That didn't work", description: errorMessage(err), type: "error" })
    } finally {
      setRetrying(null)
    }
  }

  return (
    <section aria-labelledby="gmail-imports" className="flex flex-col gap-3">
      <h2 id="gmail-imports" className="font-heading text-heading">
        Recent Gmail imports
      </h2>
      <p role="status" className="sr-only">
        {inFlight ? `${plural(inFlight, "import")} processing` : "No imports processing"}
      </p>
      {imports.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing imported into this container yet</EmptyTitle>
            <EmptyDescription>
              Messages you import into <span className="font-mono wrap-anywhere">{container}</span> show up here with their progress, and on the Add page.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul data-live={live || undefined} className="flex flex-col overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
          {imports.map((d) => {
            const Icon = d.sourceType === "email" ? MailIcon : FileTextIcon
            return (
              <li
                key={d.id}
                className="flex items-start gap-3 border-b px-4 py-3 transition-[opacity,translate] duration-200 ease-emphasized last:border-b-0 in-data-live:starting:-translate-y-1 in-data-live:starting:opacity-0"
              >
                <span aria-hidden="true" className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                  <Icon className="size-3.5" />
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <p className="line-clamp-2 text-sm font-medium break-words">{d.title || (d.sourceType === "email" ? "Email" : "Attachment")}</p>
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
                    <StatusBadge status={d.status} />
                    <span>{d.sourceType === "email" ? "Email" : "PDF attachment"}</span>
                    <time className="font-mono tabular-nums" dateTime={new Date(d.createdAt).toISOString()} title={fullDate(d.createdAt)}>
                      {ago(d.createdAt)}
                    </time>
                  </div>
                  {d.status === "failed" && d.error && <p className="text-caption break-words text-destructive-fg">{d.error}</p>}
                </div>
                {d.status === "failed" && d.canRetry && (
                  <Button size="sm" variant="outline" className="shrink-0" disabled={retrying === d.id} onClick={() => void retry(d.id)}>
                    <RotateCwIcon data-icon="inline-start" />
                    Retry
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
