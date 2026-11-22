import * as React from "react"
import { KeyRoundIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"

import { ErrorAlert } from "@/components/error-alert"
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
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { toast } from "@/components/ui/toast"
import { removeAppConnectorFn, saveAppConnectorFn, syncAppConnectorFn } from "@/lib/connector-fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import type { AppConnectorView } from "@/lib/rpc-types"

// The older built-in feeds. Any other feed sends documents and events (/docs/feeds).
const ABOUT: Record<string, string> = {
  oslo: "Oslo todos, one note per day.",
  finansial: "Finansial transactions.",
  whatsapp: "WhatsApp chats from wa-hub, one note per chat per day.",
}

function about(app: AppConnectorView) {
  if (ABOUT[app.provider]) return ABOUT[app.provider]
  let host = ""
  try {
    host = new URL(app.baseUrl).host
  } catch {}
  return host ? `Documents and events from ${host}.` : "Documents and events from your app."
}

const ID_RE = /^[a-z0-9][a-z0-9-]{0,39}$/

// "My Notes App" -> "my-notes-app"
function slug(name: string) {
  return name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
    .replace(/-+$/, "")
}

// "IDR 1000000, USD 500" <-> { IDR: 1000000, USD: 500 }
function thresholdsText(t: Record<string, number> | null) {
  if (!t) return ""
  return Object.entries(t)
    .map(([code, n]) => `${code} ${n}`)
    .join(", ")
}

function parseThresholds(text: string): Record<string, number> | string {
  const out: Record<string, number> = {}
  for (const part of text.split(/[,;\n]/).map((p) => p.trim()).filter(Boolean)) {
    const m = /^([A-Za-z]{3})\s+([\d.,_\s]+)$/.exec(part)
    const n = m ? Number(m[2]!.replace(/[,_\s]/g, "")) : NaN
    if (!m || !Number.isFinite(n) || n < 0) return `“${part}” isn't a currency and an amount, like IDR 1000000.`
    out[m[1]!.toUpperCase()] = n
  }
  return out
}

function When({ ms }: { ms: number }) {
  return (
    <time className="font-mono tabular-nums" dateTime={new Date(ms).toISOString()} title={fullDate(ms)}>
      {ago(ms)}
    </time>
  )
}

export function AppConnectorCard({
  app,
  container,
  canManage,
  secretConfigured,
  onChange,
}: {
  app: AppConnectorView
  container: string
  canManage: boolean
  secretConfigured: boolean
  onChange: () => void
}) {
  const id = `app-${app.provider}`
  const [name, setName] = React.useState(app.name)
  const [baseUrl, setBaseUrl] = React.useState(app.baseUrl)
  const [apiKey, setApiKey] = React.useState("")
  const [enabled, setEnabled] = React.useState(app.enabled)
  const [thresholds, setThresholds] = React.useState(thresholdsText(app.thresholds))
  const [errors, setErrors] = React.useState<{ baseUrl?: string; apiKey?: string; thresholds?: string; form?: string }>({})
  const [saving, setSaving] = React.useState(false)
  const [syncing, setSyncing] = React.useState(false)
  const [removeOpen, setRemoveOpen] = React.useState(false)
  const [removing, setRemoving] = React.useState(false)

  const status = !app.enabled ? (
    <Badge variant="outline">Off</Badge>
  ) : app.lastError ? (
    <Badge variant="warning">Needs attention</Badge>
  ) : (
    <Badge variant="success">Syncing hourly</Badge>
  )
  const moving = app.container !== null && app.container !== container

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    if (!/^https?:\/\/\S+$/i.test(baseUrl.trim())) next.baseUrl = "Enter the app's address, like https://app.example.com."
    if (enabled && !apiKey.trim() && !app.hasKey) next.apiKey = "Paste an API key to turn syncing on."
    const parsed = app.provider === "finansial" ? parseThresholds(thresholds) : {}
    if (typeof parsed === "string") next.thresholds = parsed
    setErrors(next)
    if (next.baseUrl || next.apiKey || next.thresholds) {
      document.getElementById(next.baseUrl ? `${id}-url` : next.apiKey ? `${id}-key` : `${id}-thresholds`)?.focus()
      return
    }
    setSaving(true)
    try {
      await saveAppConnectorFn({
        data: {
          provider: app.provider,
          ...(name.trim() && name.trim() !== app.name ? { name: name.trim() } : {}),
          container,
          baseUrl: baseUrl.trim(),
          apiKey: apiKey.trim() || undefined,
          enabled,
          ...(app.provider === "finansial" && typeof parsed !== "string" ? { thresholds: parsed } : {}),
        },
      })
      setApiKey("")
      toast.add({ title: `${name.trim() || app.name} saved`, description: enabled ? `Syncs every hour into ${container}.` : "Syncing is off.", type: "success" })
      onChange()
    } catch (err) {
      setErrors({ form: errorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      const r = await syncAppConnectorFn({ data: { provider: app.provider } })
      if (r.ok) {
        const unit = app.provider === "whatsapp" ? "message" : "event"
        const waiting = app.provider !== "finansial" && r.staged ? ` ${plural(r.staged, unit)} wait for their day to end.` : ""
        toast.add({
          title: r.documents ? `${plural(r.documents, "new note")} from ${app.name}` : `${app.name} is up to date`,
          description: `Read ${plural(r.items, "change")}.${waiting}${r.caughtUp ? "" : " More is left; the next sync carries on."}`,
          type: "success",
        })
      } else {
        toast.add({ title: `${app.name} didn't sync`, description: r.error ?? undefined, type: "error" })
      }
      onChange()
    } catch (err) {
      toast.add({ title: "That didn't work", description: errorMessage(err), type: "error" })
    } finally {
      setSyncing(false)
    }
  }

  const remove = async () => {
    setRemoving(true)
    try {
      await removeAppConnectorFn({ data: { provider: app.provider } })
      setRemoveOpen(false)
      toast.add({ title: `${app.name} removed`, description: "Notes it already wrote stay in Recall.", type: "success" })
      onChange()
    } catch (err) {
      toast.add({ title: "Couldn't remove it", description: errorMessage(err), type: "error" })
    } finally {
      setRemoving(false)
    }
  }

  const lastSync = (
    <p className="text-caption text-muted-foreground">
      {app.lastSyncAt ? (
        <>
          Last sync <When ms={app.lastSyncAt} />
          {app.lastError && app.lastSuccessAt ? (
            <>
              , last good one <When ms={app.lastSuccessAt} />
            </>
          ) : null}
          {app.container && (
            <>
              {" "}
              into <code>{app.container}</code>
            </>
          )}
          .
        </>
      ) : (
        "Never synced."
      )}
    </p>
  )

  const errorAlert = app.lastError && (
    <ErrorAlert title="The last sync failed" error={{ message: "The next sync tries again.", detail: app.lastError, retry: false }} />
  )

  if (!canManage) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{app.name}</CardTitle>
          <CardDescription>{about(app)}</CardDescription>
          <CardAction>{status}</CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {lastSync}
          {errorAlert}
          <p className="text-caption text-muted-foreground">Owners and admins set this up for the workspace.</p>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <form onSubmit={save} noValidate className="contents">
        <CardHeader>
          <CardTitle>{app.name}</CardTitle>
          <CardDescription>{about(app)}</CardDescription>
          <CardAction>{status}</CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {lastSync}
          {errorAlert}
          {!secretConfigured && (
            <Alert variant="warning">
              <KeyRoundIcon />
              <AlertTitle>Keys can't be stored yet</AlertTitle>
              <AlertDescription>
                <p>
                  Set <code>CONNECTOR_SECRET</code> (32 characters or more) on the server and restart. Recall encrypts the API key with it.
                </p>
              </AlertDescription>
            </Alert>
          )}
          <FieldGroup>
            <Field orientation="horizontal">
              <FieldContent>
                <FieldLabel htmlFor={`${id}-enabled`}>Sync every hour</FieldLabel>
              </FieldContent>
              <Switch id={`${id}-enabled`} checked={enabled} onCheckedChange={setEnabled} />
            </Field>

            <Field>
              <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
              <Input id={`${id}-name`} autoComplete="off" value={name} maxLength={60} onChange={(e) => setName(e.target.value)} />
              <FieldDescription>
                Its id is <code>{app.provider}</code>.
              </FieldDescription>
            </Field>

            <Field data-invalid={!!errors.baseUrl}>
              <FieldLabel htmlFor={`${id}-url`}>Address</FieldLabel>
              <Input
                id={`${id}-url`}
                type="url"
                inputMode="url"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
                value={baseUrl}
                maxLength={500}
                aria-invalid={!!errors.baseUrl}
                aria-describedby={errors.baseUrl ? `${id}-url-error` : undefined}
                onChange={(e) => setBaseUrl(e.target.value)}
              />
              {errors.baseUrl && <FieldError id={`${id}-url-error`}>{errors.baseUrl}</FieldError>}
            </Field>

            <Field data-invalid={!!errors.apiKey}>
              <FieldLabel htmlFor={`${id}-key`}>API key</FieldLabel>
              <Input
                id={`${id}-key`}
                type="password"
                autoComplete="off"
                spellCheck={false}
                className="font-mono placeholder:font-sans"
                placeholder={app.hasKey ? "Saved. Paste a new one to replace it." : "Paste the app's API key"}
                value={apiKey}
                maxLength={500}
                disabled={!secretConfigured}
                aria-invalid={!!errors.apiKey}
                aria-describedby={errors.apiKey ? `${id}-key-error` : undefined}
                onChange={(e) => setApiKey(e.target.value)}
              />
              {errors.apiKey ? (
                <FieldError id={`${id}-key-error`}>{errors.apiKey}</FieldError>
              ) : (
                <FieldDescription>Stored encrypted and never shown again. A read-only key is enough.</FieldDescription>
              )}
            </Field>

            {app.provider === "finansial" && (
              <Field data-invalid={!!errors.thresholds}>
                <FieldLabel htmlFor={`${id}-thresholds`}>Remember transactions from</FieldLabel>
                <Input
                  id={`${id}-thresholds`}
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                  placeholder="IDR 1000000"
                  value={thresholds}
                  maxLength={300}
                  aria-invalid={!!errors.thresholds}
                  aria-describedby={errors.thresholds ? `${id}-thresholds-error` : undefined}
                  onChange={(e) => setThresholds(e.target.value)}
                />
                {errors.thresholds ? (
                  <FieldError id={`${id}-thresholds-error`}>{errors.thresholds}</FieldError>
                ) : (
                  <FieldDescription>
                    Like <code className="whitespace-nowrap">IDR 1000000, USD 100</code>.
                  </FieldDescription>
                )}
              </Field>
            )}

            {errors.form && (
              <Alert variant="destructive">
                <AlertTitle>Couldn't save</AlertTitle>
                <AlertDescription>{errors.form}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
        </CardContent>
        <CardFooter className="flex-wrap gap-2">
          <Button type="submit" disabled={saving}>
            {saving && <Spinner data-icon="inline-start" />}
            Save
          </Button>
          <Button type="button" variant="outline" disabled={!app.hasKey || syncing} onClick={() => void sync()}>
            {syncing ? <Spinner data-icon="inline-start" /> : <RefreshCwIcon data-icon="inline-start" />}
            Sync now
          </Button>
          <Button type="button" variant="ghost" onClick={() => setRemoveOpen(true)}>
            <Trash2Icon data-icon="inline-start" />
            Remove
          </Button>
          <p className="min-w-0 text-caption text-muted-foreground">
            {moving ? (
              <>
                Saving here moves it from <code>{app.container}</code> to <code>{container}</code>.
              </>
            ) : (
              <>
                Notes go into <code>{container}</code>.
              </>
            )}
          </p>
        </CardFooter>
      </form>
      <AlertDialog open={removeOpen} onOpenChange={setRemoveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {app.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Recall stops syncing it and forgets its address and key. Notes it already wrote stay. Lines still waiting for their day to end are dropped.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction variant="danger" disabled={removing} data-agent-confirm="Removes this feed" onClick={() => void remove()}>
              {removing && <Spinner data-icon="inline-start" />}
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}

// AddFeedCard sets up a new feed: any app that serves /api/v1/changes with
// document and event items (/docs/feeds).
export function AddFeedCard({
  container,
  taken,
  secretConfigured,
  onChange,
}: {
  container: string
  taken: string[]
  secretConfigured: boolean
  onChange: () => void
}) {
  const id = "add-feed"
  const [open, setOpen] = React.useState(false)
  const [name, setName] = React.useState("")
  const [feedId, setFeedId] = React.useState("")
  const [idEdited, setIdEdited] = React.useState(false)
  const [baseUrl, setBaseUrl] = React.useState("")
  const [apiKey, setApiKey] = React.useState("")
  const [errors, setErrors] = React.useState<{ name?: string; feedId?: string; baseUrl?: string; form?: string }>({})
  const [saving, setSaving] = React.useState(false)
  const shownId = idEdited ? feedId : slug(name)

  const reset = () => {
    setOpen(false)
    setName("")
    setFeedId("")
    setIdEdited(false)
    setBaseUrl("")
    setApiKey("")
    setErrors({})
  }

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    if (!name.trim()) next.name = "Give it a name, like Notes."
    if (!ID_RE.test(shownId)) next.feedId = "Use lowercase letters, digits and dashes, like notes."
    else if (taken.includes(shownId)) next.feedId = "That id is in use. Pick another."
    if (!/^https?:\/\/\S+$/i.test(baseUrl.trim())) next.baseUrl = "Enter the app's address, like https://app.example.com."
    setErrors(next)
    const first = next.name ? "name" : next.feedId ? "id" : next.baseUrl ? "url" : null
    if (first) {
      document.getElementById(`${id}-${first}`)?.focus()
      return
    }
    setSaving(true)
    try {
      await saveAppConnectorFn({
        data: { provider: shownId, name: name.trim(), container, baseUrl: baseUrl.trim(), apiKey: apiKey.trim() || undefined, enabled: !!apiKey.trim() },
      })
      toast.add({
        title: `${name.trim()} added`,
        description: apiKey.trim() ? `Syncs every hour into ${container}.` : "Paste its API key to turn syncing on.",
        type: "success",
      })
      reset()
      onChange()
    } catch (err) {
      setErrors({ form: errorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  if (!open) {
    return (
      <div>
        <Button type="button" variant="outline" onClick={() => setOpen(true)}>
          <PlusIcon data-icon="inline-start" />
          Add a feed
        </Button>
      </div>
    )
  }

  return (
    <Card>
      <form onSubmit={save} noValidate className="contents">
        <CardHeader>
          <CardTitle>Add a feed</CardTitle>
          <CardDescription>
            Any app that serves <code>/api/v1/changes</code> with documents and events.{" "}
            <a href="/docs/feeds" className="underline underline-offset-2">
              What it sends
            </a>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <FieldGroup>
            <Field data-invalid={!!errors.name}>
              <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
              <Input
                id={`${id}-name`}
                autoComplete="off"
                value={name}
                maxLength={60}
                aria-invalid={!!errors.name}
                aria-describedby={errors.name ? `${id}-name-error` : undefined}
                onChange={(e) => setName(e.target.value)}
              />
              {errors.name && <FieldError id={`${id}-name-error`}>{errors.name}</FieldError>}
            </Field>

            <Field data-invalid={!!errors.feedId}>
              <FieldLabel htmlFor={`${id}-id`}>Id</FieldLabel>
              <Input
                id={`${id}-id`}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
                value={shownId}
                maxLength={40}
                aria-invalid={!!errors.feedId}
                aria-describedby={errors.feedId ? `${id}-id-error` : `${id}-id-help`}
                onChange={(e) => {
                  setIdEdited(true)
                  setFeedId(e.target.value.toLowerCase())
                }}
              />
              {errors.feedId ? (
                <FieldError id={`${id}-id-error`}>{errors.feedId}</FieldError>
              ) : (
                <FieldDescription id={`${id}-id-help`}>Part of every note's source. It can't change later.</FieldDescription>
              )}
            </Field>

            <Field data-invalid={!!errors.baseUrl}>
              <FieldLabel htmlFor={`${id}-url`}>Address</FieldLabel>
              <Input
                id={`${id}-url`}
                type="url"
                inputMode="url"
                autoComplete="off"
                spellCheck={false}
                className="font-mono placeholder:font-sans"
                placeholder="https://app.example.com"
                value={baseUrl}
                maxLength={500}
                aria-invalid={!!errors.baseUrl}
                aria-describedby={errors.baseUrl ? `${id}-url-error` : undefined}
                onChange={(e) => setBaseUrl(e.target.value)}
              />
              {errors.baseUrl && <FieldError id={`${id}-url-error`}>{errors.baseUrl}</FieldError>}
            </Field>

            <Field>
              <FieldLabel htmlFor={`${id}-key`}>API key</FieldLabel>
              <Input
                id={`${id}-key`}
                type="password"
                autoComplete="off"
                spellCheck={false}
                className="font-mono placeholder:font-sans"
                placeholder={secretConfigured ? "Paste the app's API key" : "Set CONNECTOR_SECRET on the server first"}
                value={apiKey}
                maxLength={500}
                disabled={!secretConfigured}
                onChange={(e) => setApiKey(e.target.value)}
              />
              <FieldDescription>Stored encrypted. With a key, it syncs every hour.</FieldDescription>
            </Field>

            {errors.form && (
              <Alert variant="destructive">
                <AlertTitle>Couldn't add it</AlertTitle>
                <AlertDescription>{errors.form}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
        </CardContent>
        <CardFooter className="flex-wrap gap-2">
          <Button type="submit" disabled={saving}>
            {saving && <Spinner data-icon="inline-start" />}
            Add feed
          </Button>
          <Button type="button" variant="ghost" onClick={reset}>
            Cancel
          </Button>
          <p className="min-w-0 text-caption text-muted-foreground">
            Notes go into <code>{container}</code>.
          </p>
        </CardFooter>
      </form>
    </Card>
  )
}
