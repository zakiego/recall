// Pieces shared by the Connect and Agent instructions pages: the key card, the
// client picker as choice cards, and snippets that mark where the key goes.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { BracesIcon, GlobeIcon, KeyRoundIcon, MonitorIcon, SquareTerminalIcon, TriangleAlertIcon, type LucideIcon } from "lucide-react"

import { CopyButton } from "@/components/copy-button"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { CodeBlock, CodeBlockActions, CodeBlockContent, CodeBlockHeader, CodeBlockTitle } from "@/components/ui/code-block"
import { Field, FieldContent, FieldDescription, FieldLabel, FieldTitle } from "@/components/ui/field"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { PagePending } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { CLIENTS, KEY_PLACEHOLDER, keyNameFor, type ClientId } from "@/lib/connect"
import { createKey } from "@/lib/fns"
import { errorMessage } from "@/lib/format"
import { cn } from "@/lib/utils"

// `at` is when the key arrived, so its marks flash only at that moment and
// not every time a snippet mounts later.
export type CreatedKey = { key: string; name: string; at: number }

// A key made on a page lives only in that page's state, so it's gone when you
// leave. `fresh` is true for the first second, while the marks flash.
export function useCreatedKey() {
  const [created, setCreated] = React.useState<CreatedKey | null>(null)
  const key = created?.key ?? KEY_PLACEHOLDER
  const fresh = created !== null && Date.now() - created.at < 1000
  return { created, setCreated, key, fresh }
}

export function ConnectPending({ label }: { label: string }) {
  return (
    <PagePending label={label}>
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-24 w-full rounded-xl" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </PagePending>
  )
}

// "Where does your agent run?" as choice cards, shared by both pages.
export function ClientPicker({ id, title, value, onChange }: { id: string; title: string; value: ClientId; onChange: (v: ClientId) => void }) {
  return (
    <>
      <h2 id={id} className="font-heading text-heading">
        {title}
      </h2>
      <ChoiceCards
        labelledBy={id}
        value={value}
        onChange={onChange}
        className="sm:grid-cols-2 xl:grid-cols-4"
        options={CLIENTS.map((c) => ({ value: c.id, title: c.label, hint: CLIENT_HINTS[c.id].hint, Icon: CLIENT_HINTS[c.id].Icon }))}
      />
    </>
  )
}

export const CLIENT_HINTS: Record<ClientId, { hint: string; Icon: LucideIcon }> = {
  "claude-code": { hint: "One command in your terminal", Icon: SquareTerminalIcon },
  "claude-desktop": { hint: "The Mac or Windows app", Icon: MonitorIcon },
  mcp: { hint: "Cursor, Windsurf and other MCP apps", Icon: GlobeIcon },
  rest: { hint: "curl, scripts, any HTTP client", Icon: BracesIcon },
}

// A pick-one choice as big labelled cards: easier to read as a choice than a
// segmented control, and the whole card is the hit target.
export function ChoiceCards<T extends string>({
  labelledBy,
  value,
  onChange,
  options,
  className,
}: {
  labelledBy: string
  value: T
  onChange: (v: T) => void
  options: { value: T; title: string; hint: string; Icon?: LucideIcon }[]
  className?: string
}) {
  const id = React.useId()
  return (
    <RadioGroup aria-labelledby={labelledBy} value={value} onValueChange={(v) => onChange(v as T)} className={cn("grid gap-3", className)}>
      {options.map((o) => (
        <FieldLabel key={o.value} htmlFor={`${id}-${o.value}`} className="cursor-pointer">
          <Field orientation="horizontal" className="items-start gap-3 p-3.5!">
            {o.Icon && <o.Icon aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-muted-foreground group-has-data-checked/field-label:text-foreground" />}
            <FieldContent>
              <FieldTitle id={`${id}-${o.value}-title`}>{o.title}</FieldTitle>
              <FieldDescription id={`${id}-${o.value}-hint`} className="text-caption">
                {o.hint}
              </FieldDescription>
            </FieldContent>
            <RadioGroupItem
              value={o.value}
              id={`${id}-${o.value}`}
              aria-labelledby={`${id}-${o.value}-title`}
              aria-describedby={`${id}-${o.value}-hint`}
              className="mt-0.5"
            />
          </Field>
        </FieldLabel>
      ))}
    </RadioGroup>
  )
}

export function Code({ children }: { children: React.ReactNode }) {
  return <code data-chip="" className="whitespace-nowrap">{children}</code>
}

export function Note({ children }: { children: React.ReactNode }) {
  return <p className="max-w-[72ch] text-sm text-pretty text-muted-foreground">{children}</p>
}

// The key (or its placeholder) is marked wherever it appears, so you can see
// what got filled in.
export function Marked({ text, secret, fresh }: { text: string; secret?: string; fresh?: boolean }) {
  if (!secret || !text.includes(secret)) return <>{text}</>
  const parts = text.split(secret)
  return (
    <>
      {parts.map((part, i) => (
        <React.Fragment key={i}>
          {part}
          {i < parts.length - 1 && (
            <mark data-key={secret === KEY_PLACEHOLDER ? "placeholder" : fresh ? "fresh" : "real"} className="key-mark rounded-sm bg-foreground/10 px-0.5 text-foreground">
              {secret}
            </mark>
          )}
        </React.Fragment>
      ))}
    </>
  )
}

export function Snippet({ title, text, secret, fresh }: { title: string; text: string; secret?: string; fresh?: boolean }) {
  return (
    <CodeBlock size="sm">
      <CodeBlockHeader>
        <CodeBlockTitle>{title}</CodeBlockTitle>
        <CodeBlockActions>
          <CopyButton text={text} variant="ghost" name={`Copy ${title}`} />
        </CodeBlockActions>
      </CodeBlockHeader>
      <CodeBlockContent wrap>
        <Marked text={text} secret={secret} fresh={fresh} />
      </CodeBlockContent>
    </CodeBlock>
  )
}

export function KeyCard({
  what,
  canManage,
  workspace,
  username,
  created,
  onCreated,
}: {
  /** What on this page shows the key, e.g. "config" or "setup prompt". */
  what: string
  canManage: boolean
  workspace: string
  username: string
  created: CreatedKey | null
  onCreated: (k: CreatedKey) => void
}) {
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()
  // The create button unmounts when the key arrives; move focus to the key so
  // keyboard and screen reader users land on it instead of on the page body.
  const createdRef = React.useRef<HTMLDivElement>(null)
  React.useEffect(() => {
    if (created) createdRef.current?.focus()
  }, [created])

  if (!canManage) {
    return (
      <Alert>
        <KeyRoundIcon />
        <AlertTitle>Ask an admin for a key</AlertTitle>
        <AlertDescription>
          <p>
            Only owners and admins of {workspace} create keys. Once you have one, swap it in for <Code>{KEY_PLACEHOLDER}</Code> in the {what} below.
          </p>
        </AlertDescription>
      </Alert>
    )
  }

  if (created) {
    return (
      <Card
        className="enter-in"
        ref={createdRef}
        tabIndex={-1}
        role="region"
        aria-labelledby="connect-created-key"
      >
        <CardHeader>
          <CardTitle id="connect-created-key">
            Key for <span className="font-mono wrap-anywhere">{created.name}</span>
          </CardTitle>
          <CardDescription>Filled into every snippet below.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <CodeBlock size="sm">
            <CodeBlockHeader>
              <CodeBlockTitle>API key</CodeBlockTitle>
              <CodeBlockActions>
                <CopyButton text={created.key} variant="ghost" name="Copy API key" />
              </CodeBlockActions>
            </CodeBlockHeader>
            <CodeBlockContent wrap className="select-all">
              {created.key}
            </CodeBlockContent>
          </CodeBlock>
          <Alert variant="warning">
            <TriangleAlertIcon />
            <AlertTitle>You won't see this key again</AlertTitle>
            <AlertDescription>
              Recall keeps only its hash, and this page forgets it when you leave. Lost it? Revoke it under{" "}
              <Link to="/settings/keys" className="underline underline-offset-4">
                API keys
              </Link>{" "}
              and make another.
            </AlertDescription>
          </Alert>
        </CardContent>
      </Card>
    )
  }

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      // The date part is read here, on the client, so server and browser never disagree about "today".
      const res = await createKey({ data: { name: keyNameFor(username) } })
      onCreated({ key: res.key, name: res.name, at: Date.now() })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>API key</CardTitle>
        <CardDescription className="text-pretty">Fills every snippet below. One key works for every client.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col items-start gap-3">
        <Button onClick={() => void create()} disabled={busy}>
          {busy ? <Spinner data-icon="inline-start" /> : <KeyRoundIcon data-icon="inline-start" />}
          Create API key
        </Button>
        {error && (
          <Alert variant="destructive">
            <TriangleAlertIcon />
            <AlertTitle>Couldn't create the key</AlertTitle>
            <AlertDescription>
              {error} Try again, or make one under{" "}
              <Link to="/settings/keys" className="underline underline-offset-4">
                API keys
              </Link>
              .
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}
