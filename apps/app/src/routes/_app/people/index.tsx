import * as React from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { BookUserIcon, PlusIcon, SearchIcon, XIcon } from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { PageHeader } from "@/components/page-header"
import { PossibleDuplicates } from "@/components/person-duplicates"
import { Button } from "@/components/ui/button"
import { CodeBlock, CodeBlockActions, CodeBlockContent, CodeBlockHeader, CodeBlockTitle } from "@/components/ui/code-block"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { PagePending, PageHeaderSkeleton, TableSkeleton } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useContainer } from "@/hooks/use-container"
import { getPeople } from "@/lib/fns"
import { ago, fullDate, plural } from "@/lib/format"

type Sort = "recent" | "name"

const BACKFILL = "recall backfill-people"

export const Route = createFileRoute("/_app/people/")({
  // ?q= filters by name, alias, role, organization, email or handle. ?sort=name lists A to Z.
  validateSearch: z.object({ q: z.string().max(200).optional(), sort: z.enum(["recent", "name"]).optional() }),
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c, q: search.q, sort: search.sort }),
  loader: ({ deps, context }) => getPeople({ data: { container: deps.c ?? context.defaultContainer, q: deps.q, sort: deps.sort } }),
  pendingComponent: PeopleSkeleton,
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "People · Recall" }] }),
  component: PeoplePage,
})

function PeoplePage() {
  const container = useContainer()
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const sort: Sort = search.sort ?? "recent"
  const [q, setQ] = React.useState(search.q ?? "")
  const qRef = React.useRef<HTMLInputElement>(null)
  const clear = () => {
    setQ("")
    qRef.current?.focus()
  }

  // The URL follows the field a beat behind typing, so each keystroke isn't a round trip.
  React.useEffect(() => {
    const next = q.trim() || undefined
    if (next === search.q) return
    const t = setTimeout(() => void navigate({ search: (prev) => ({ ...prev, q: next }), replace: true }), 200)
    return () => clearTimeout(t)
  }, [q, search.q, navigate])

  const filtered = !!search.q
  const none = data.total === 0 && !filtered

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="People"
        description={
          <>
            People named in <code>{container}</code>.
          </>
        }
      />

      {!none && <PossibleDuplicates container={container} />}

      {none ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BookUserIcon />
            </EmptyMedia>
            <EmptyTitle>No people yet</EmptyTitle>
            <EmptyDescription>For facts saved before people existed, run this once:</EmptyDescription>
          </EmptyHeader>
          <CodeBlock size="sm" className="max-w-md text-start">
            <CodeBlockHeader>
              <CodeBlockTitle>Server shell</CodeBlockTitle>
              <CodeBlockActions>
                <CopyButton text={BACKFILL} variant="ghost" name="Copy backfill command" />
              </CodeBlockActions>
            </CodeBlockHeader>
            <CodeBlockContent wrap>{BACKFILL}</CodeBlockContent>
          </CodeBlock>
          <EmptyContent className="flex-row justify-center">
            <Button render={<Link to="/add" search={{ c: container }} />}>
              <PlusIcon data-icon="inline-start" />
              Add content
            </Button>
            <Button variant="outline" render={<Link to="/" search={{ c: container }} />}>
              See the facts
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <section aria-label="People" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <InputGroup className="w-full sm:w-72">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                ref={qRef}
                type="search"
                aria-label="Find a person"
                placeholder="Name, role, company or email…"
                className="[&::-webkit-search-cancel-button]:hidden"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
              {q && (
                <InputGroupAddon align="inline-end">
                  <InputGroupButton size="icon-xs" aria-label="Clear filter" onClick={clear}>
                    <XIcon />
                  </InputGroupButton>
                </InputGroupAddon>
              )}
            </InputGroup>
            <ToggleGroup
              aria-label="Sort"
              variant="outline"
              value={[sort]}
              onValueChange={(v: string[]) =>
                v[0] && void navigate({ search: (prev) => ({ ...prev, sort: v[0] === "name" ? "name" : undefined }), replace: true })
              }
            >
              <ToggleGroupItem value="recent">Last mentioned</ToggleGroupItem>
              <ToggleGroupItem value="name">A to Z</ToggleGroupItem>
            </ToggleGroup>
            <p className="ms-auto text-caption text-muted-foreground tabular-nums" role="status">
              {plural(data.total, "person", "people")}
            </p>
          </div>

          <div className="overflow-hidden rounded-xl bg-card shadow-(--shadow-border)">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="md:w-[30%]">Name</TableHead>
                  <TableHead className="hidden md:table-cell">Newest fact</TableHead>
                  <TableHead className="w-20 text-end">Facts</TableHead>
                  <TableHead className="hidden w-36 sm:table-cell">Last mentioned</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.people.length === 0 && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={4} className="py-10 text-center text-sm whitespace-normal text-muted-foreground">
                      Nobody matches “{search.q}”.{" "}
                      <Button variant="link" className="h-auto p-0" onClick={clear}>
                        Clear the search
                      </Button>{" "}
                      or{" "}
                      <Button variant="link" className="h-auto p-0" render={<Link to="/search" search={{ c: container, q: search.q }} />}>
                        search the facts instead
                      </Button>
                    </TableCell>
                  </TableRow>
                )}
                {data.people.map((p) => {
                  const sub = [p.role, p.organization].filter(Boolean).join(" · ")
                  return (
                    <TableRow key={p.id} className="relative [&>td]:align-top">
                      <TableCell className="py-2.5 whitespace-normal">
                        {/* The name link covers the row, so the whole row is one target. */}
                        <Link
                          to="/people/$id"
                          params={{ id: p.id }}
                          search={{ c: container }}
                          className="block text-sm font-medium break-words underline-offset-4 after:absolute after:inset-0 hover:underline"
                        >
                          {p.name}
                        </Link>
                        {sub && (
                          <p className="mt-0.5 line-clamp-2 text-caption break-words text-muted-foreground" title={sub}>
                            {sub}
                          </p>
                        )}
                        {p.latestFact && <p className="mt-1 line-clamp-2 text-caption text-pretty text-muted-foreground md:hidden">{p.latestFact}</p>}
                      </TableCell>
                      <TableCell className="hidden py-2.5 whitespace-normal md:table-cell">
                        {p.latestFact ? (
                          <p className="line-clamp-2 text-sm text-pretty break-words" title={p.latestFact}>
                            {p.latestFact}
                          </p>
                        ) : (
                          <span className="text-caption text-muted-foreground">
                            {p.lastMentionedAt !== null ? "Only in older versions of facts" : "No facts"}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="py-2.5 text-end text-sm tabular-nums">{p.factCount}</TableCell>
                      <TableCell className="hidden py-2.5 sm:table-cell">
                        {p.lastMentionedAt ? (
                          <time
                            className="text-caption text-muted-foreground tabular-nums"
                            dateTime={new Date(p.lastMentionedAt).toISOString()}
                            title={fullDate(p.lastMentionedAt)}
                          >
                            {ago(p.lastMentionedAt)}
                          </time>
                        ) : (
                          <span className="text-caption text-muted-foreground">Never</span>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
          {data.nextOffset !== null && (
            <p className="text-center text-caption text-muted-foreground">
              Showing the first {data.people.length}. Search to find the rest.
            </p>
          )}
        </section>
      )}
    </div>
  )
}

function PeopleSkeleton() {
  return (
    <PagePending label="Loading people…">
      <PageHeaderSkeleton />
      <div className="flex items-center gap-2">
        <Skeleton className="h-8 w-full max-w-72" />
        <Skeleton className="h-8 w-28" />
        <Skeleton className="h-8 w-14" />
      </div>
      <TableSkeleton
        columns={[
          { className: "md:w-[30%]", sub: true },
          { className: "hidden md:table-cell" },
          { className: "w-20", short: "w-4", align: "end" },
          { className: "hidden w-36 sm:table-cell", short: "w-20" },
        ]}
      />
    </PagePending>
  )
}
