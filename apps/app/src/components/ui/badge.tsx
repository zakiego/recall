import { renderElement, type PartProps } from "@ui-sdk/react/primitive"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

// Status badges pair a tint with a word (and usually an icon), never the tint
// alone. Each tone reads its own role tokens, so "success" is the same green
// in a log row and on a tool call.
const badgeVariants = cva(
  "group/badge inline-flex h-5 w-fit [a]:transition-[background-color,color,scale] [a]:duration-150 [a]:active:scale-[0.96] shrink-0 items-center justify-center gap-1 overflow-hidden rounded-full border border-transparent px-2 text-xs font-medium whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3! [&>svg]:shrink-0 has-[>svg]:ps-1.5",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground",
        soft: "bg-primary-subtle text-primary-fg",
        success: "bg-success-bg text-success-fg",
        warning: "bg-warning-bg text-warning-fg",
        destructive: "bg-destructive-bg text-destructive-fg",
        secondary: "bg-secondary text-secondary-foreground",
        outline: "border-border text-muted-foreground [a]:hover:bg-muted [a]:hover:text-foreground",
        ghost: "text-muted-foreground",
        link: "text-primary-fg underline underline-offset-4",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Badge({
  className,
  variant = "default",
  render,
  ...props
}: Omit<PartProps<"span">, "className"> & { className?: string } & VariantProps<typeof badgeVariants>) {
  return renderElement(
    "span",
    { ...props, "data-slot": "badge", "data-variant": variant, className: cn(badgeVariants({ variant }), className) },
    render
  )
}

export { Badge, badgeVariants }
