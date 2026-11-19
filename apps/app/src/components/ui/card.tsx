/**
 * The card: a white surface lifted off the page by a shadow ring rather than a
 * border, so it reads as depth and the lines inside it stay free to mean
 * structure.
 *
 * The title is an `h2`. Every card sits under the page's one `h1`, so the
 * outline a screen reader walks is page, then section, with nothing skipped.
 */

import * as React from "react"

import { cn } from "@/lib/utils"

function Card({
  className,
  size = "default",
  ...props
}: React.ComponentProps<"div"> & { size?: "default" | "sm" }) {
  return (
    <div
      data-slot="card"
      data-size={size}
      className={cn(
        "group/card flex min-w-0 flex-col gap-(--card-gap) overflow-hidden rounded-xl bg-card py-(--card-spacing) text-sm text-card-foreground border border-transparent shadow-(--shadow-border) [--card-gap:--spacing(4)] [--card-spacing:--spacing(5)] has-data-[slot=card-footer]:pb-0 data-[size=sm]:[--card-gap:--spacing(2)] data-[size=sm]:[--card-spacing:--spacing(4)]",
        className
      )}
      {...props}
    />
  )
}

function CardHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-header"
      className={cn(
        "group/card-header @container/card-header grid auto-rows-min items-start gap-x-4 gap-y-1 px-(--card-spacing) has-data-[slot=card-action]:grid-cols-[1fr_auto] has-data-[slot=card-description]:grid-rows-[auto_auto] [.border-b]:pb-(--card-spacing)",
        className
      )}
      {...props}
    />
  )
}

function CardTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return (
    <h2
      data-slot="card-title"
      className={cn(
        "font-heading text-heading group-data-[size=sm]/card:text-sm",
        className
      )}
      {...props}
    />
  )
}

// `max-w-[70ch]` caps the measure. These cards run the full width of a desktop
// window, which put explanatory copy at ~180 characters per line, far past the
// 60 to 75 where the eye still finds the next line reliably. Capped here rather
// than per page so no card can reintroduce it. `text-pretty` keeps the last
// line from stranding a single word.
function CardDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="card-description"
      className={cn(
        "max-w-[70ch] text-sm text-pretty text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}

function CardAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-action"
      className={cn(
        "col-start-2 row-span-2 row-start-1 flex items-center gap-2 self-start justify-self-end",
        className
      )}
      {...props}
    />
  )
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-content"
      className={cn("px-(--card-spacing)", className)}
      {...props}
    />
  )
}

function CardFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-footer"
      className={cn(
        "flex items-center border-t bg-muted/50 px-(--card-spacing) py-3",
        className
      )}
      {...props}
    />
  )
}

export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardAction,
  CardDescription,
  CardContent,
}
