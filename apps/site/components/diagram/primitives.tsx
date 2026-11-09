import type { LucideIcon } from "lucide-react"
import type { CSSProperties, ReactNode } from "react"
import { Card, CardContent } from "@/components/ui/card"
import { Item, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item"
import { cn } from "@/lib/cn"

// Building blocks for the docs diagrams. Everything is vertical first: forks
// and joins stay inside one column of equal-width cells, so a diagram never
// needs sideways scrolling at 390px. Rows that sit side by side switch on the
// diagram's own width (`@2xl`, a container query), not the viewport, because
// the docs sidebar eats most of a tablet screen. Lines and arrowheads use
// --muted-foreground and group frames use --input, both above 3:1 against
// the card in light and dark.

// The gap between fork columns. The bracket line math below depends on it.
const GAP = "0.75rem"

/**
 * The frame. The drawing is hidden from assistive tech and the caption
 * carries the same information as text, so it's read once. The caption comes
 * from the MDX children, which keeps it in the per-page markdown export.
 */
export function Diagram({ children, description }: { children: ReactNode; description: ReactNode }) {
  return (
    <figure className="not-prose @container my-6">
      <Card>
        <CardContent>
          <div aria-hidden="true" className="mx-auto flex w-full max-w-2xl flex-col py-2 @2xl:px-2">
            {children}
          </div>
        </CardContent>
      </Card>
      <figcaption className="sr-only">{description}</figcaption>
    </figure>
  )
}

/** One box: an icon, a short name, and an optional real name in mono. */
export function Node({
  icon: Icon,
  title,
  detail,
  stack = false,
  optional = false,
  className,
}: {
  icon: LucideIcon
  title: ReactNode
  /** The machine name (route, file, table). Rendered in Geist Mono. */
  detail?: ReactNode
  /** Icon above the text, centered. For cells in a 2 or 3 column fork. */
  stack?: boolean
  /** Dashed outline for a step that only runs sometimes. */
  optional?: boolean
  className?: string
}) {
  return (
    <Item
      variant="outline"
      size="sm"
      className={cn(
        "min-w-0",
        stack ? "flex-col justify-center gap-2 text-center" : "mx-auto max-w-sm",
        optional && "border-dashed",
        className,
      )}
    >
      <ItemMedia
        variant="icon"
        className={cn(stack && "group-has-data-[slot=item-description]/item:translate-y-0 group-has-data-[slot=item-description]/item:self-center")}
      >
        <span className="flex size-8 items-center justify-center rounded-md bg-muted text-foreground">
          <Icon aria-hidden="true" />
        </span>
      </ItemMedia>
      <ItemContent className={cn("min-w-0 gap-0.5", stack && "items-center")}>
        <ItemTitle className={cn("line-clamp-none", stack && "justify-center")}>{title}</ItemTitle>
        {detail && (
          <ItemDescription
            translate="no"
            className={cn("line-clamp-none font-mono text-xs wrap-break-word", stack && "text-center")}
          >
            {detail}
          </ItemDescription>
        )}
      </ItemContent>
    </Item>
  )
}

/** A dashed region around boxes that belong together (a host, a loop). */
export function Group({
  icon: Icon,
  label,
  children,
  className,
}: {
  icon?: LucideIcon
  label: string
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex min-w-0 flex-col rounded-xl border border-dashed border-input p-3 @2xl:p-4", className)}>
      <div className="mb-3 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        {Icon && <Icon aria-hidden="true" className="size-3.5 shrink-0" />}
        {label}
      </div>
      {children}
    </div>
  )
}

function Head({ direction = "down" }: { direction?: "down" | "right" }) {
  return direction === "down" ? (
    <span className="size-0 border-x-[5px] border-t-[6px] border-x-transparent border-t-muted-foreground" />
  ) : (
    <span className="size-0 border-y-[5px] border-l-[6px] border-y-transparent border-l-muted-foreground" />
  )
}

function EdgeLabel({ children }: { children: ReactNode }) {
  return <span className="px-2 py-1 text-center text-xs text-muted-foreground wrap-break-word">{children}</span>
}

/**
 * A straight arrow down. With `responsive`, it points right once the diagram
 * is wide enough (`@2xl`), for a row that sits side by side. `grow` lets the
 * horizontal arrow take the free space in its row.
 */
export function Arrow({
  label,
  responsive = false,
  grow = false,
  className,
  style,
}: {
  label?: ReactNode
  responsive?: boolean
  grow?: boolean
  className?: string
  style?: CSSProperties
}) {
  const down = (
    <div className={cn("flex flex-col items-center", responsive && "@2xl:hidden", className)} style={style}>
      <span className="h-5 w-px bg-muted-foreground" />
      {label && (
        <>
          <EdgeLabel>{label}</EdgeLabel>
          <span className="h-2 w-px bg-muted-foreground" />
        </>
      )}
      <Head />
    </div>
  )
  if (!responsive) return down
  return (
    <>
      {down}
      <div className={cn("hidden min-w-12 flex-col items-center justify-center self-center @2xl:flex", grow && "flex-1")}>
        {label && <EdgeLabel>{label}</EdgeLabel>}
        <div className="flex w-full items-center">
          <span className="h-px flex-1 bg-muted-foreground" />
          <Head direction="right" />
        </div>
      </div>
    </>
  )
}

/** Horizontal inset from the edge to the center of the first or last column. */
function bracketInset(cols: number) {
  return `calc((100% - ${cols - 1} * ${GAP}) / ${cols * 2})`
}

const GRID_COLS: Record<number, string> = {
  2: "grid-cols-2",
  3: "grid-cols-3",
}

/** The line that ties the column centers together, under or over a row. */
function Bracket({ cols, className }: { cols: number; className?: string }) {
  const inset = bracketInset(cols)
  return <span className={cn("absolute h-px bg-muted-foreground", className)} style={{ left: inset, right: inset }} />
}

/**
 * One line splits into 2 or 3 columns, each with its own arrow and optional
 * label. With `join`, the columns merge back into one arrow at the bottom.
 * Cells stretch to the tallest one so the row reads as one level.
 */
export function Fork({
  labels,
  join = false,
  children,
}: {
  labels?: (ReactNode | undefined)[]
  join?: boolean
  children: ReactNode[]
}) {
  const cols = children.length
  return (
    <div className="flex flex-col">
      <div className="relative flex flex-col">
        <span className="mx-auto h-3 w-px bg-muted-foreground" />
        <Bracket cols={cols} className="top-3" />
        <div className={cn("grid", GRID_COLS[cols])} style={{ gap: GAP }}>
          {children.map((child, i) => (
            <div key={i} className="flex min-w-0 flex-col [&>[data-slot=item]]:flex-1">
              <Arrow label={labels?.[i]} />
              {child}
              {join && <span className="mx-auto h-3 w-px shrink-0 bg-muted-foreground" />}
            </div>
          ))}
        </div>
      </div>
      {join && (
        <div className="relative">
          <Bracket cols={cols} className="top-0" />
          <Arrow />
        </div>
      )}
    </div>
  )
}

/** Columns that start side by side and merge into one arrow. For inputs that all lead to the same place. */
export function Join({ children }: { children: ReactNode[] }) {
  const cols = children.length
  return (
    <div className="flex flex-col">
      <div className={cn("grid", GRID_COLS[cols])} style={{ gap: GAP }}>
        {children.map((child, i) => (
          <div key={i} className="flex min-w-0 flex-col [&>[data-slot=item]]:flex-1">
            {child}
            <span className="mx-auto h-3 w-px shrink-0 bg-muted-foreground" />
          </div>
        ))}
      </div>
      <div className="relative">
        <Bracket cols={cols} className="top-0" />
        <Arrow />
      </div>
    </div>
  )
}

