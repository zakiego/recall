import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { FileTextIcon, FileUpIcon, GlobeIcon, InboxIcon, MailIcon, MessageSquareTextIcon, MoreHorizontalIcon, RotateCwIcon, Trash2Icon, TypeIcon } from "lucide-react"

import { PageHeader } from "@/components/page-header"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
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
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </div>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Add · Recall" }] }),
  component: AddPage,
})

type Doc = Awaited<ReturnType<typeof getDocuments>>[number]
const POLL_MS = 2_000
const MAX_FILE_MB = 20

function AddPage() {
  const container = useContainer()
  const docs = Route.useLoaderData()
  const router = useRouter()
  const pending = docs.some((d) => d.status === "queued" || d.status === "processing")
  const { d: linked } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const [marked, setMarked] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!linked) return
    if (docs.some((d) => d.id === linked)) {
      setMarked(linked)
      requestAnimationFrame(() => document.getElementById(`doc-${linked}`)?.scrollIntoView({ block: "center" }))
    } else {
      toast.add({ title: "That document isn't in the latest 50", type: "info" })
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
            Give Recall something to learn about <span className="font-mono break-all text-foreground">{container}</span>. Facts are pulled out in the
            background, usually within a few seconds, and replace older facts they contradict.
          </>
        }
      />
      <AddForm container={container} onAdded={() => void router.invalidate()} />
      <RecentDocuments docs={docs} marked={marked} onChange={() => void router.invalidate()} />
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
  const fileInput = React.useRef<HTMLInputElement>(null)

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
        if (fileInput.current) fileInput.current.value = ""
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
      <CardContent className="flex flex-col gap-4">
      <Tabs value={tab} onValueChange={(v: string) => setTab(v)}>
        <TabsList>
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
                value={text}
                aria-invalid={invalid}
                placeholder={"I switched to Puma running shoes, the Adidas ones hurt my knee.\n\nuser: aku pindah ke Banjarbaru bulan lalu"}
                onChange={(e) => setText(e.target.value)}
              />
              {invalid ? <FieldError>{error}</FieldError> : <FieldDescription>Notes, a chat excerpt, anything. English or Indonesian.</FieldDescription>}
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
              {invalid ? <FieldError>{error}</FieldError> : <FieldDescription>Recall fetches it, keeps the readable text and drops the page chrome.</FieldDescription>}
            </Field>
          </FieldGroup>
        </TabsContent>
        <TabsContent value="file" className="pt-3">
          <FieldGroup>
            <Field data-invalid={invalid}>
              <FieldLabel htmlFor="add-file">File</FieldLabel>
              <Input
                ref={fileInput}
                id="add-file"
                type="file"
                accept=".pdf,.txt,.md,.markdown,.csv,.json,.html,.htm,application/pdf,text/*"
                aria-invalid={invalid}
                onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              />
              {invalid ? (
                <FieldError>{error}</FieldError>
              ) : (
                <FieldDescription>PDF, Markdown, plain text, CSV, JSON or HTML, up to {MAX_FILE_MB} MB.</FieldDescription>
              )}
            </Field>
          </FieldGroup>
        </TabsContent>
      </Tabs>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-caption text-muted-foreground">
          Goes to <span className="font-mono break-all text-foreground">{container}</span>
        </p>
        <Button type="submit" disabled={busy}>
          {busy && <Spinner data-icon="inline-start" />}
          Remember this
        </Button>
      </div>
      </CardContent>
      </Card>
    </form>
  )
}

const TYPE_ICON = { text: TypeIcon, chat: MessageSquareTextIcon, url: GlobeIcon, file: FileTextIcon, email: MailIcon } as const

function RecentDocuments({ docs, marked, onChange }: { docs: Doc[]; marked: string | null; onChange: () => void }) {
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
        <ul className="flex flex-col overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
          {docs.map((d) => (
            <DocRow key={d.id} doc={d} marked={marked === d.id} onChange={onChange} />
          ))}
        </ul>
      )}
    </section>
  )
}

function DocRow({ doc, marked, onChange }: { doc: Doc; marked: boolean; onChange: () => void }) {
  const Icon = TYPE_ICON[doc.sourceType]
  const ex = doc.extract
  const [busy, setBusy] = React.useState(false)

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
      className="flex scroll-mt-20 items-start gap-3 border-b px-4 py-3 transition-[opacity,translate,background-color] duration-200 ease-emphasized last:border-b-0 starting:-translate-y-1 starting:opacity-0 data-marked:bg-muted"
    >
      <span aria-hidden="true" className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
        <Icon className="size-3.5" />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="truncate text-sm font-medium" title={doc.title ?? doc.url ?? undefined}>
          {doc.title || doc.url || doc.preview || "Untitled"}
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
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {doc.status === "failed" && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void act(() => retryDocument({ data: { id: doc.id } }), "Queued again")}>
            <RotateCwIcon data-icon="inline-start" />
            Retry
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${doc.title ?? "this source"}`} disabled={busy} />}>
            <MoreHorizontalIcon />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuGroup>
              <DropdownMenuItem variant="destructive" onClick={() => void act(() => removeDocument({ data: { id: doc.id } }), "Source removed. Its facts stay.")}>
                <Trash2Icon />
                Remove source
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  )
}
