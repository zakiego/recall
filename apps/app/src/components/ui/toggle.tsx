import { Toggle as TogglePrimitive } from "@ui-sdk/react/toggle"
import type * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const toggleVariants = cva(
  "group/toggle inline-flex cursor-pointer items-center justify-center gap-1 rounded-lg text-sm font-medium whitespace-nowrap transition-[background-color,border-color,color,scale] duration-150 ease-out outline-none select-none hover:bg-muted hover:text-foreground active:not-disabled:scale-[0.97] disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive data-pressed:bg-muted [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        // Pressed needs more than bg-muted: in dark mode it equals the popover and dialog surface.
        outline:
          "border border-input bg-transparent text-muted-foreground hover:bg-muted data-pressed:border-foreground/50 data-pressed:bg-accent data-pressed:text-foreground",
      },
      size: {
        default:
          "h-8 min-w-8 px-2.5 pointer-coarse:h-10 has-data-[icon=inline-end]:pe-2 has-data-[icon=inline-start]:ps-2",
        sm: "h-7 min-w-7 rounded-md px-2.5 text-caption pointer-coarse:h-9 has-data-[icon=inline-end]:pe-1.5 has-data-[icon=inline-start]:ps-1.5 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-9 min-w-9 px-2.5 pointer-coarse:h-10 has-data-[icon=inline-end]:pe-2 has-data-[icon=inline-start]:ps-2",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

function Toggle({
  className,
  variant = "default",
  size = "default",
  ...props
}: Omit<React.ComponentProps<typeof TogglePrimitive>, "className"> & { className?: string } & VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive
      className={cn(toggleVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Toggle, toggleVariants }
