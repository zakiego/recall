import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { EllipsisIcon, FileTextIcon, FileUpIcon, GlobeIcon, InboxIcon, MailIcon, MessageSquareTextIcon, RotateCwIcon, Trash2Icon, TypeIcon } from "lucide-react"
import { FileInput, FileInputTrigger } from "@ui-sdk/react/file-input"

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
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PagePending } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { addText, getDocuments, removeDocument, retryDocument, uploadFile } from "@/lib/fns"
import { ago, errorMessage, fullDate, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"
import { z } from "zod"

export const Route = createFileRoute("/_app/add")({
  // ?d= (from Ask's document citations) scrolls to and marks that document.
  validateSearch: z.object({ d: z.string().max(64).optional() }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c }),
  loader: ({ deps, context }) => getDocuments({ data: { container: deps.c ?? context.defaultContainer } }),
  pendingComponent: () => (
    <PagePending label="Loading documents…">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Add · Recall" }] }),
  component: AddPage,
})

type Doc = Awaited<ReturnType<typeof getDocuments>>["documents"][number]
// Pages past the first, loaded with "Load more". `cursor` continues after the last of them.
type Older = { container: string; rows: Doc[]; cursor: string | null }
const POLL_MS = 2_000
const MAX_FILE_MB = 20
const FILE_TYPES = [".pdf", ".txt", ".md", ".markdown", ".csv", ".json", ".html", ".htm", "application/pdf", "text/*"]

// The `count` rows that follow `cursor`, fetched in as few requests as the page limit allows.
async function fetchRange(container: string, cursor: string | null, count: number): Promise<Older> {
  const rows: Doc[] = []
  let next = cursor
  while (next && rows.length < count) {
    const page = await getDocuments({ data: { container, cursor: next, limit: Math.min(200, count - rows.length) } })
    rows.push(...page.documents)
    next = page.nextCursor
  }
  return { container, rows, cursor: next }
}

function AddPage() {
  const container = useContainer()
  const first = Route.useLoaderData()
  const router = useRouter()
  const [loadedOlder, setOlder] = React.useState<Older | null>(null)
  // Another container starts again from its first page.
  const older = loadedOlder?.container === container ? loadedOlder : null
  const [loadingMore, setLoadingMore] = React.useState(false)
  // Bumped by every fetch of older pages, so only the newest response lands.
  const seq = React.useRef(0)

  // A refresh replaces the first page. Pages already loaded past it are
  // fetched again as one range from the new first page, so statuses stay
  // current and a new or removed source doesn't leave a gap or a stale row.
  const loaded = older?.rows.length ?? 0
  React.useEffect(() => {
    if (!loaded) return
    const mine = ++seq.current
    fetchRange(container, first.nextCursor, loaded).then(
      (range) => mine === seq.current && setOlder(range),
      () => {},
    )
    // Only a new first page refetches, not a longer `older`.
  }, [first])

  const docs = React.useMemo(() => {
    const seen = new Set(first.documents.map((d) => d.id))
    return [...first.documents, ...(older?.rows ?? []).filter((d) => !seen.has(d.id))]
  }, [first, older])
  const cursor = older ? older.cursor : first.nextCursor
  const pending = docs.some((d) => d.status === "queued" || d.status === "processing")

  const loadMore = async () => {
    if (!cursor) return
    const mine = ++seq.current
    setLoadingMore(true)
    try {
      const page = await getDocuments({ data: { container, cursor } })
      if (mine === seq.current) setOlder({ container, rows: [...(older?.rows ?? []), ...page.documents], cursor: page.nextCursor })
    } catch (err) {
      toast.add({ title: "Couldn't load more", description: errorMessage(err), type: "error" })
    } finally {
      setLoadingMore(false)
    }
  }
  const { d: linked } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const [marked, setMarked] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!linked) return
    if (docs.some((d) => d.id === linked)) {
      setMarked(linked)
      requestAnimationFrame(() => document.getElementById(`doc-${linked}`)?.scrollIntoView({ block: "center" }))
    } else {
      toast.add({ title: "That source isn't in the ones loaded", description: "Load more at the bottom of the list to find it.", type: "info" })
    }
    void navigate({ search: (prev) => ({ ...prev, d: undefined }), replace: true })
  }, [linked, docs, navigate])

  // Poll only while something is in flight, and stop when the tab is hidden.
  React.useEffect(() => {
    if (!pending) return
    const t = setInterval(() => {
      if (document.visibilityState === "visible") void router.invalidate()
    }, POLL_MS)
    return () => clearInterval(t)
  }, [pending, router])

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Add"
        description={
          <>
            Give Recall something to learn about <code>{container}</code>.
          </>
        }
      />
      <AddForm container={container} onAdded={() => void router.invalidate()} />
      <RecentDocuments
        docs={docs}
        total={first.total}
        marked={marked}
        onChange={() => void router.invalidate()}
        more={cursor ? { loading: loadingMore, load: () => void loadMore() } : null}
      />
    </div>
  )
}

function AddForm({ container, onAdded }: { container: string; onAdded: () => void }) {
  const [tab, setTab] = React.useState("text")
  const [text, setText] = React.useState("")
  const [url, setUrl] = React.useState("")
  const [file, setFile] = React.useState<File | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  React.useEffect(() => setError(undefined), [tab])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(undefined)
    try {
      setBusy(true)
      if (tab === "text") {
        if (!text.trim()) return setError("Paste or type something to remember.")
        await addText({ data: { container, kind: "text", value: text } })
        setText("")
      } else if (tab === "url") {
        if (!url.trim()) return setError("Enter the address of a page or PDF.")
        await addText({ data: { container, kind: "url", value: url.trim() } })
        setUrl("")
      } else {
        if (!file) return setError("Choose a file first.")
        if (file.size > MAX_FILE_MB * 1024 * 1024) return setError(`That file is over ${MAX_FILE_MB} MB.`)
        const form = new FormData()
        form.set("file", file)
        form.set("container", container)
        await uploadFile({ data: form })
        setFile(null)
      }
      toast.add({ title: "Queued", description: "Facts will show up in Memories in a few seconds.", type: "success" })
      onAdded()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const invalid = error !== undefined

  return (
    <form onSubmit={submit}>
      <Card>
      <CardContent>
      <Tabs value={tab} onValueChange={(v) => v && setTab(v)}>
        <TabsList aria-label="Content type">
          <TabsTrigger value="text">
            <TypeIcon />
            Text
          </TabsTrigger>
          <TabsTrigger value="url">
            <GlobeIcon />
            URL
          </TabsTrigger>
          <TabsTrigger value="file">
            <FileUpIcon />
            File
          </TabsTrigger>
        </TabsList>
        <TabsContent value="text" className="pt-3">
          <FieldGroup>
            <Field data-invalid={invalid}>
              <FieldLabel htmlFor="add-text">Text or conversation</FieldLabel>
              <Textarea
                id="add-text"
                rows={7}
                className="min-h-36"
                value={text}
                aria-invalid={invalid}
                placeholder={"I switched to Puma running shoes, the Adidas ones hurt my knee.\n\nuser: aku pindah ke Bandung bulan lalu"}
                onChange={(e) => setText(e.target.value)}
              />
              {invalid && <FieldError>{error}</FieldError>}
            </Field>
          </FieldGroup>
        </TabsContent>
        <TabsContent value="url" className="pt-3">
          <FieldGroup>
            <Field data-invalid={invalid}>
              <FieldLabel htmlFor="add-url">Page or PDF address</FieldLabel>
              <Input
                id="add-url"
                type="url"
                inputMode="url"
                autoComplete="url"
                placeholder="https://example.com/article"
                value={url}
                aria-invalid={invalid}
                onChange={(e) => setUrl(e.target.value)}
              />
              {invalid && <FieldError>{error}</FieldError>}
            </Field>
          </FieldGroup>
        </TabsContent>
        <TabsContent value="file" className="pt-3">
          <FieldGroup>
            <Field data-invalid={invalid}>
              <FieldLabel id="add-file-label" htmlFor="add-file">
                File
              </FieldLabel>
              <FileInput
                accept={FILE_TYPES}
                value={file ? [file] : []}
                onValueChange={(files) => {
                  setFile(files[0] ?? null)
                  setError(undefined)
                }}
                onReject={() => setError("Choose a PDF, Markdown, text, CSV, JSON or HTML file.")}
                className="flex min-w-0 flex-wrap items-center gap-3"
              >
                <FileInputTrigger
                  id="add-file"
                  aria-labelledby="add-file-label add-file"
                  aria-invalid={invalid || undefined}
                  render={<Button type="button" variant="outline" />}
                >
                  <FileUpIcon data-icon="inline-start" />
                  {file ? "Replace file" : "Choose file"}
                </FileInputTrigger>
                <span className={cn("min-w-0 text-sm wrap-anywhere", file ? "text-foreground" : "text-muted-foreground")}>{file?.name ?? "No file chosen"}</span>
              </FileInput>
              {invalid ? (
                <FieldError>{error}</FieldError>
              ) : (
                <FieldDescription>PDF, Markdown, plain text, CSV, JSON or HTML, up to {MAX_FILE_MB} MB.</FieldDescription>
              )}
            </Field>
          </FieldGroup>
        </TabsContent>
      </Tabs>
      </CardContent>
      <CardFooter className="flex-wrap justify-between gap-3">
        <p className="min-w-0 text-caption text-muted-foreground">
          Goes to <code>{container}</code>
        </p>
        <Button type="submit" disabled={busy}>
          {busy && <Spinner data-icon="inline-start" />}
          Remember this
        </Button>
      </CardFooter>
      </Card>
    </form>
  )
}

const TYPE_ICON = { text: TypeIcon, chat: MessageSquareTextIcon, url: GlobeIcon, file: FileTextIcon, email: MailIcon } as const

function RecentDocuments({
  docs,
  total,
  marked,
  onChange,
  more,
}: {
  docs: Doc[]
  total: number | null
  marked: string | null
  onChange: () => void
  more: { loading: boolean; load: () => void } | null
}) {
  // Rows rendered with the page are just there; only rows that arrive while
  // the page is open slide in, so a visit doesn't replay them.
  const [live, setLive] = React.useState(false)
  React.useEffect(() => setLive(true), [])
  return (
    <section aria-labelledby="recent" className="flex flex-col gap-3">
      <h2 id="recent" className="font-heading text-heading">
        Recent sources
      </h2>
      <p role="status" className="sr-only">
        {docs.filter((d) => d.status === "queued" || d.status === "processing").length || "No"} sources processing
      </p>
      {docs.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon />
            </EmptyMedia>
            <EmptyTitle>Nothing added to this container yet</EmptyTitle>
            <EmptyDescription>What you add above, and what agents send through the API, shows up here with its progress.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <ul data-live={live || undefined} className="flex flex-col overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
            {docs.map((d) => (
              <DocRow key={d.id} doc={d} marked={marked === d.id} onChange={onChange} />
            ))}
          </ul>
          {(more || (total !== null && total > docs.length)) && (
            <div className="flex flex-col items-center gap-2">
              {total !== null && (
                <p className="text-caption text-muted-foreground tabular-nums">
                  Showing {docs.length.toLocaleString("en")} of {plural(total, "source")}
                </p>
              )}
              {more && (
                <Button variant="outline" disabled={more.loading} onClick={more.load}>
                  {more.loading && <Spinner data-icon="inline-start" />}
                  Load more
                </Button>
              )}
            </div>
          )}
        </>
      )}
    </section>
  )
}

function DocRow({ doc, marked, onChange }: { doc: Doc; marked: boolean; onChange: () => void }) {
  const Icon = TYPE_ICON[doc.sourceType]
  const ex = doc.extract
  const [busy, setBusy] = React.useState(false)
  const [confirming, setConfirming] = React.useState(false)
  const name = doc.title || doc.url || doc.preview || "Untitled"

  const act = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true)
    try {
      await fn()
      toast.add({ title: done, type: "success" })
      onChange()
    } catch (err) {
      toast.add({ title: "That didn't work", description: errorMessage(err), type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <li
      id={`doc-${doc.id}`}
      data-marked={marked || undefined}
      className="flex scroll-mt-20 items-start gap-3 border-b px-4 py-3 transition-[opacity,translate,background-color] duration-200 ease-emphasized last:border-b-0 in-data-live:starting:-translate-y-1 in-data-live:starting:opacity-0 data-marked:bg-muted"
    >
      <span aria-hidden="true" className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
        <Icon className="size-3.5" />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="line-clamp-2 text-sm font-medium wrap-anywhere">
          {name}
        </p>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
          <StatusBadge status={doc.status} />
          {doc.status === "done" && ex && (
            <span className="tabular-nums">
              {ex.added + ex.updated + ex.invalidated + ex.conflicts === 0
                ? "No new facts"
                : [
                    ex.added && `${ex.added} new`,
                    ex.updated && `${ex.updated} updated`,
                    ex.invalidated && `${ex.invalidated} retired`,
                    ex.conflicts && `${ex.conflicts} need review`,
                  ]
                    .filter(Boolean)
                    .join(", ")}
            </span>
          )}
          {doc.status === "done" && <span className="tabular-nums">{plural(doc.memoryCount, "current fact")}</span>}
          <time dateTime={new Date(doc.createdAt).toISOString()} title={fullDate(doc.createdAt)}>
            {ago(doc.createdAt)}
          </time>
        </div>
        {doc.status === "failed" && doc.error && <p className={cn("text-caption break-words text-destructive-fg")}>{doc.error}</p>}
        {/* Under the error, not beside the title, so a long error keeps the row's full width on a phone. */}
        {doc.status === "failed" && (
          <Button
            size="sm"
            variant="outline"
            className="mt-1 self-start"
            disabled={busy}
            onClick={() => void act(() => retryDocument({ data: { id: doc.id } }), "Queued again")}
          >
            <RotateCwIcon data-icon="inline-start" />
            Retry
          </Button>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${name}`} disabled={busy} />}>
            <EllipsisIcon aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuGroup>
              <DropdownMenuItem variant="destructive" onSelect={() => setConfirming(true)}>
                <Trash2Icon aria-hidden="true" />
                Remove source
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle className="wrap-anywhere">Remove “{name}”?</AlertDialogTitle>
              <AlertDialogDescription>Its facts stay. The source text and link are gone.</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Keep it</AlertDialogCancel>
              <AlertDialogAction
                variant="danger"
                data-agent-confirm={`Removes the source “${name}”`}
                disabled={busy}
                onClick={async () => {
                  await act(() => removeDocument({ data: { id: doc.id } }), "Source removed. Its facts stay.")
                  setConfirming(false)
                }}
              >
                {busy && <Spinner data-icon="inline-start" />}
                Remove source
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </li>
  )
}
