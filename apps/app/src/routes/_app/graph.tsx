import * as React from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { NetworkIcon, PlusIcon } from "lucide-react"

import { useTheme } from "@/components/theme-provider"
import { PageHeader } from "@/components/page-header"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { getGraph } from "@/lib/fns"
import { plural } from "@/lib/format"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"

export const Route = createFileRoute("/_app/graph")({
  loaderDeps: ({ search }) => ({ c: (search as { c?: string }).c }),
  loader: ({ deps, context }) => getGraph({ data: { container: deps.c ?? context.defaultContainer } }),
  pendingComponent: () => (
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading graph">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-[60svh] w-full rounded-xl" />
    </div>
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
    memFill: get("--card"),
    memFillHover: get("--accent"),
    memStrokeDefault: get("--border-strong"),
    accent: get("--primary"),
    textPrimary: get("--foreground"),
    textSecondary: get("--muted-foreground"),
    textMuted: get("--muted-foreground"),
    edgeDocument: get("--border-strong"),
    edgeDerives: get("--muted-foreground"),
    edgeUpdates: get("--success"),
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

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Graph"
        description={
          <>
            Sources and the facts they produced. Lines between facts are versions: a newer fact replacing an older one.
            {data.capped && ` Showing the latest ${data.documents.length} of ${data.totalDocuments} sources.`}
          </>
        }
      />
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
        <section aria-label={`Graph of ${plural(data.documents.length, "source")} and ${plural(facts, "fact")}`} className="flex flex-col gap-2">
          <div className="relative h-[min(70svh,48rem)] min-h-96 overflow-hidden rounded-xl bg-background shadow-(--shadow-border)">
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
                <Alert variant="destructive">
                  <AlertTitle>The graph couldn't load</AlertTitle>
                  <AlertDescription>{loadError.message}</AlertDescription>
                  <AlertAction>
                    <Button size="sm" variant="outline" onClick={() => window.location.reload()}>
                      Reload
                    </Button>
                  </AlertAction>
                </Alert>
              </div>
            ) : (
              <Skeleton className="size-full rounded-none" />
            )}
          </div>
          <p className="text-caption text-muted-foreground tabular-nums">
            {plural(data.documents.length, "source")} · {plural(facts, "fact")}, including older versions. Drag to pan, scroll to zoom.
          </p>
        </section>
      )}
    </div>
  )
}
