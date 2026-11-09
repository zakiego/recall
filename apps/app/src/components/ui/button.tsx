/**
 * The button, from shadcn's base-nova, retuned for this dashboard.
 *
 * Every variant presses to 0.96 on a transition, so a release mid-press eases
 * back instead of snapping. `static` turns that off where motion would
 * distract. Focus is not styled here: one unlayered rule in index.css draws
 * the ring for every control, so no variant can forget it.
 *
 * On a coarse pointer the default sizes grow to 40px, the target a thumb
 * needs; a mouse keeps the denser 32px.
 */

import { Button as ButtonPrimitive } from "@base-ui/react/button"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const buttonVariants = cva(
  "group/button inline-flex shrink-0 cursor-pointer items-center justify-center rounded-lg border border-transparent bg-clip-padding text-sm font-medium whitespace-nowrap transition-[scale,background-color,color,box-shadow] duration-150 ease-out outline-none select-none disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "bg-primary text-primary-foreground shadow-[0_1px_2px_oklch(0_0_0/0.12),inset_0_1px_0_oklch(1_0_0/0.12)] hover:bg-primary-hover",
        outline:
          "bg-card text-foreground shadow-(--shadow-border) hover:bg-accent hover:shadow-(--shadow-border-hover) aria-expanded:bg-accent",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-accent aria-expanded:bg-accent",
        ghost:
          "text-foreground hover:bg-accent aria-expanded:bg-accent",
        destructive:
          "bg-destructive-bg text-destructive-fg shadow-[0_0_0_1px_var(--destructive-border)] hover:bg-[color-mix(in_oklch,var(--destructive-bg),var(--destructive)_12%)]",
        danger:
          "bg-destructive-solid text-white shadow-[0_1px_2px_oklch(0_0_0/0.12)] hover:bg-destructive-solid-hover",
        "destructive-ghost":
          "text-destructive-fg hover:bg-destructive-bg",
        link: "h-auto! px-0! text-primary-fg underline decoration-from-font underline-offset-[3px] [text-underline-position:from-font] hover:decoration-2",
      },
      size: {
        default:
          "h-8 gap-1.5 px-3 pointer-coarse:h-10 has-data-[icon=inline-end]:pe-2.5 has-data-[icon=inline-start]:ps-2.5",
        xs: "h-6 gap-1 rounded-md px-2 text-xs [&_svg:not([class*='size-'])]:size-3",
        sm: "h-7 gap-1.5 rounded-md px-2.5 text-[0.8125rem] pointer-coarse:h-9 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 gap-2 px-4",
        icon: "size-8 pointer-coarse:size-10",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-7 rounded-md pointer-coarse:size-9",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

function Button({
  className,
  variant = "default",
  size = "default",
  static: isStatic = false,
  ...props
}: ButtonPrimitive.Props &
  VariantProps<typeof buttonVariants> & { static?: boolean }) {
  return (
    <ButtonPrimitive
      data-slot="button"
      className={cn(
        buttonVariants({ variant, size }),
        !isStatic && variant !== "link" && "active:not-disabled:scale-[0.96]",
        className
      )}
      {...props}
    />
  )
}

export { Button, buttonVariants }
