/**
 * The top of every page: its one `h1`, a sentence on what the page is for,
 * and the page's actions on the trailing edge.
 *
 * The title used to live in the header bar with each page repeating it as a
 * card title underneath. One heading, owned by the page, reads better and
 * gives the outline a single root.
 */

import * as React from "react"

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        <h1 className="font-heading text-title">{title}</h1>
        {description && (
          <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {actions && (
        <div className="flex flex-wrap items-center gap-2 sm:-mt-0.5">{actions}</div>
      )}
    </div>
  )
}
