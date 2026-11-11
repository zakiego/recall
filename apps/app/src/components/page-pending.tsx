import * as React from "react"

import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { cn } from "@/lib/utils"

/**
 * The frame every route's pendingComponent renders in. The router only shows
 * it once a load takes longer than a second, so it fades in rather than
 * replacing the old page in one frame. When it unmounts, the page that
 * replaces it fades in too.
 *
 * That second fade is the only route-level motion in the dashboard, and it's
 * tied to the skeleton on purpose: a fast page switch never shows a skeleton,
 * so it never fades. Only a load slow enough to need a placeholder gets one,
 * and there the fade turns "grey blocks, then a page" into one change.
 */
export function PagePending({ label, className, children }: { label: string; className?: string; children: React.ReactNode }) {
  // A layout effect, so the fade starts in the commit that swaps the skeleton
  // for the page, before the browser paints the page at full opacity.
  React.useLayoutEffect(() => revealMain, [])
  // No live region here: one that mounts already busy and already holding its
  // text is usually not read. RouteProgress owns the spoken "Loading page…".
  return (
    <div aria-busy="true" className={cn("pending-in flex flex-col gap-6", className)}>
      <span className="sr-only">{label}</span>
      {children}
    </div>
  )
}

function revealMain() {
  const main = document.getElementById("main-content")
  if (typeof main?.animate !== "function") return
  // Reduced motion in this app means everything lands at once (see the
  // reduce block at the end of app.css), and WAAPI doesn't see that rule.
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
  main.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 200, easing: "cubic-bezier(0.2, 0, 0, 1)" })
}

/**
 * The page title, its one-line description and its action button, in
 * PageHeader's own layout, so the action wraps the same way on a phone.
 */
export function PageHeaderSkeleton({ description = "w-96", action = false }: { description?: string; action?: boolean }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        <Skeleton className="h-[1.8rem] w-40" />
        <Skeleton className={cn("h-5 max-w-full", description)} />
      </div>
      {action && <Skeleton className="h-8 w-28" />}
    </div>
  )
}

export type SkeletonColumn = {
  /** The real column's width and breakpoint classes, set on both the head and the cells. */
  className?: string
  /** Second, shorter line under the first (a role, a badge row). */
  sub?: boolean
  /** A fixed-size value (a count, a date, an icon) instead of a line of text. */
  short?: string
  /** No label in the header (an expand or actions column). */
  bare?: boolean
  align?: "end"
}

// Text lines of different lengths, so the rows read as text and not as a grid of bars.
const LINE_WIDTHS = ["w-3/5", "w-4/5", "w-2/5", "w-3/4", "w-1/2", "w-2/3"]
const SUB_WIDTHS = ["w-24", "w-16", "w-28", "w-20", "w-24", "w-14"]

/**
 * A placeholder that is the real Table, so the header, row heights, padding
 * and card edge match the page that replaces it and nothing jumps.
 */
export function TableSkeleton({
  columns,
  rows = 6,
  header = true,
  className,
}: {
  columns: SkeletonColumn[]
  rows?: number
  header?: boolean
  className?: string
}) {
  return (
    <div className={cn("overflow-hidden rounded-xl bg-card shadow-(--shadow-border)", className)}>
      <Table>
        {header && (
          <TableHeader>
            <TableRow>
              {columns.map((c, i) => (
                <TableHead key={i} className={c.className}>
                  {!c.bare && <Skeleton className={cn("h-3 w-14", c.align === "end" && "ms-auto")} />}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
        )}
        <TableBody>
          {Array.from({ length: rows }, (_, r) => (
            <TableRow key={r} className="hover:bg-transparent">
              {columns.map((c, i) => (
                <TableCell key={i} className={c.className}>
                  <div className={cn("flex flex-col gap-2 py-1", c.align === "end" && "items-end")}>
                    <Skeleton className={cn("h-4", c.short ?? LINE_WIDTHS[(r + i) % LINE_WIDTHS.length])} />
                    {c.sub && <Skeleton className={cn("h-3", SUB_WIDTHS[(r + i) % SUB_WIDTHS.length])} />}
                  </div>
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
