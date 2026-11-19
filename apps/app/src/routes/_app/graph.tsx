import * as React from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { NetworkIcon, PlusIcon } from "lucide-react"

import { useTheme } from "@/components/theme-provider"
import { ErrorAlert } from "@/components/error-alert"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { PagePending } from "@/components/page-pending"
import { Skeleton } from "@/components/ui/skeleton"
import { getGraph } from "@/lib/fns"
import { plural } from "@/lib/format"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"

export const Route = createFileRoute("/_app/graph")({
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c }),
  loader: ({ deps, context }) => getGraph({ data: { container: deps.c ?? context.defaultContainer } }),
  pendingComponent: () => (
    <PagePending label="Loading graph…">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-[60svh] w-full rounded-xl" />
    </PagePending>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Graph · Recall" }] }),
  component: GraphPage,
})

type GraphModule = typeof import("@supermemory/memory-graph")
type Colors = NonNullable<React.ComponentProps<GraphModule["MemoryGraph"]>["colors"]>

// The graph draws on a canvas, which can't read CSS variables, so the theme
// tokens are resolved to concrete colors once per theme.
function readColors(): Colors {
  const probe = document.createElement("span")
  document.body.append(probe)
  const get = (v: string) => {
    probe.style.color = `var(${v})`
    return getComputedStyle(probe).color
  }
  const c: Colors = {
    bg: get("--background"),
    dotColor: get("--border"),
    docFill: get("--card"),
    docStroke: get("--border-strong"),
    docInnerFill: get("--muted"),
    memFill: get("--muted"),
    memFillHover: get("--accent"),
    memStrokeDefault: get("--muted-foreground"),
    accent: get("--primary"),
    textPrimary: get("--foreground"),
    textSecondary: get("--muted-foreground"),
    textMuted: get("--muted-foreground"),
    edgeDocument: get("--border-strong"),
    edgeDerives: get("--muted-foreground"),
    edgeUpdates: get("--foreground"),
    edgeExtends: get("--primary"),
    memBorderForgotten: get("--destructive"),
    memBorderExpiring: get("--warning"),
    memBorderRecent: get("--success"),
    glowColor: get("--primary"),
    iconColor: get("--muted-foreground"),
    popoverBg: get("--popover"),
    popoverBorder: get("--border"),
    popoverTextPrimary: get("--popover-foreground"),
    popoverTextSecondary: get("--muted-foreground"),
    popoverTextMuted: get("--muted-foreground"),
    controlBg: get("--card"),
    controlBorder: get("--border"),
  }
  probe.remove()
  return c
}

function GraphPage() {
  const container = useContainer()
  const data = Route.useLoaderData()
  const { resolvedTheme } = useTheme()
  const [mod, setMod] = React.useState<GraphModule>()
  const [colors, setColors] = React.useState<Colors>()
  const [loadError, setLoadError] = React.useState<Error | null>(null)

  React.useEffect(() => {
    import("@supermemory/memory-graph").then(setMod, (e) => setLoadError(e instanceof Error ? e : new Error(String(e))))
  }, [])
  React.useEffect(() => {
    // Wait a frame so the theme class has been applied.
    const id = requestAnimationFrame(() => setColors(readColors()))
    return () => cancelAnimationFrame(id)
  }, [resolvedTheme])

  const facts = data.documents.reduce((n, d) => n + d.memories.length, 0)
  const sources = data.documents.length

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Graph" />
      {data.documents.length === 0 ? (
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <NetworkIcon />
            </EmptyMedia>
            <EmptyTitle>No graph yet</EmptyTitle>
            <EmptyDescription>Once something is added to this container, its sources and facts show up here.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button render={<Link to="/add" search={{ c: container }} />}>
              <PlusIcon data-icon="inline-start" />
              Add content
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <section aria-label={`Graph of ${plural(sources, "source")} and ${plural(facts, "fact")}`} className="flex flex-col gap-2">
          {/* The library pins its controls and legend bottom-start with inline styles; this caps them inside the canvas. */}
          <div className="relative h-[min(70svh,48rem)] min-h-96 overflow-hidden rounded-xl bg-background shadow-(--shadow-border) [&_[style*='bottom:_16px'][style*='z-index:_20']]:max-h-[calc(100%-2rem)] [&_[style*='bottom:_16px'][style*='z-index:_20']]:overflow-y-auto [&_[style*='bottom:_16px'][style*='z-index:_20']>*]:shrink-0">
            {mod && colors ? (
              <mod.MemoryGraph
                key={`${container}-${resolvedTheme}`}
                documents={data.documents}
                isLoading={false}
                error={loadError}
                variant="console"
                colors={colors}
                appearance={{ documentNodes: "stacked", connections: "emphasized" }}
              />
            ) : loadError ? (
              <div className="p-6">
                <ErrorAlert
                  title="The graph couldn't load"
                  error={{ message: "The graph code didn't download. Reload the page.", detail: loadError.message, retry: true }}
                  onRetry={() => window.location.reload()}
                />
              </div>
            ) : (
              <Skeleton className="size-full rounded-none" />
            )}
          </div>
          <p className="text-caption text-pretty text-muted-foreground tabular-nums">
            {plural(sources, "source")} · {plural(facts, "fact")}
            {data.capped && ` · newest ${data.shownDocuments.toLocaleString("en")} of ${data.totalDocuments.toLocaleString("en")} sources`}. Drag to pan.
            Pinch or scroll to zoom.
          </p>
        </section>
      )}
    </div>
  )
}
