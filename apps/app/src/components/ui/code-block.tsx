// A code block without a syntax highlighter: the Connect page shows short configs and
// commands, not source files. Copy lives in the project's CopyButton, and the
// code is a block instead of a line grid so inline marks stay in the text.
import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const codeBlockVariants = cva(
  "group/code-block relative flex w-full min-w-0 flex-col overflow-hidden rounded-lg text-sm data-[size=sm]:text-xs",
  {
    variants: {
      variant: {
        default: "border border-border bg-muted/40 text-foreground",
        destructive: "border border-destructive/30 text-destructive dark:border-destructive/40",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
)

function CodeBlock({
  className,
  variant = "default",
  size = "default",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof codeBlockVariants> & { size?: "default" | "sm" }) {
  return <div data-slot="code-block" data-focus-within-ring="" data-variant={variant} data-size={size} className={cn(codeBlockVariants({ variant }), className)} {...props} />
}

function CodeBlockHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="code-block-header"
      className={cn(
        "flex min-w-0 shrink-0 items-center gap-2 px-3 pt-3 text-muted-foreground group-data-[size=sm]/code-block:px-2.5 group-data-[size=sm]/code-block:pt-2.5 group-data-[variant=destructive]/code-block:text-destructive *:has-[>[data-slot=code-block-title]]:min-w-0",
        className,
      )}
      {...props}
    />
  )
}

function CodeBlockTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="code-block-title"
      className={cn(
        "min-w-0 truncate font-sans text-sm font-medium text-foreground group-data-[size=sm]/code-block:text-xs group-data-[variant=destructive]/code-block:text-destructive [&_svg]:pointer-events-none [&_svg]:inline-block [&_svg]:align-middle [&_svg:not([class*='size-'])]:size-3.5 *:[svg]:me-1.5",
        className,
      )}
      {...props}
    />
  )
}

function CodeBlockActions({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="code-block-actions" className={cn("-my-1 ms-auto -me-1.5 flex shrink-0 items-center gap-1", className)} {...props} />
}

function CodeBlockContent({
  className,
  children,
  wrap = false,
  ...props
}: React.ComponentProps<"pre"> & {
  wrap?: boolean
}) {
  return (
    <pre
      data-slot="code-block-content"
      data-wrap={wrap || undefined}
      data-focus-within=""
      dir="ltr"
      // Only a block that scrolls sideways needs to be a tab stop.
      tabIndex={wrap ? undefined : 0}
      className={cn(
        "min-h-0 min-w-0 overflow-auto px-3 py-3 text-start font-mono leading-relaxed outline-none group-has-data-[slot=code-block-footer]/code-block:pb-2 group-has-data-[slot=code-block-header]/code-block:pt-2 group-data-[size=sm]/code-block:px-2.5 group-data-[size=sm]/code-block:py-2.5 data-wrap:wrap-anywhere data-wrap:whitespace-pre-wrap",
        className,
      )}
      {...props}
    >
      <code data-slot="code-block-code" className="block min-h-lh min-w-max in-data-wrap:min-w-0">
        {children}
      </code>
    </pre>
  )
}

function CodeBlockFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="code-block-footer"
      className={cn("flex min-w-0 shrink-0 items-center gap-2 px-3 pb-3 text-muted-foreground group-data-[size=sm]/code-block:px-2.5 group-data-[size=sm]/code-block:pb-2.5", className)}
      {...props}
    />
  )
}

export { CodeBlock, CodeBlockHeader, CodeBlockTitle, CodeBlockActions, CodeBlockContent, CodeBlockFooter }
