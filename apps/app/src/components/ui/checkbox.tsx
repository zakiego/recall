"use client"

import * as React from "react"
import { Checkbox as CheckboxPrimitive, CheckboxIndicator } from "@ui-sdk/react/checkbox"

import { cn } from "@/lib/utils"
import { CheckIcon, MinusIcon } from "lucide-react"

function Checkbox({
  className,
  checked,
  indeterminate,
  ...props
}: Omit<React.ComponentProps<typeof CheckboxPrimitive>, "className" | "children"> & {
  className?: string
  indeterminate?: boolean
}) {
  // The indicator stays mounted while it fades out, so it keeps the last shape
  // it showed instead of flipping from a dash to a tick on the way out.
  const [dash, setDash] = React.useState(!!indeterminate)
  const shown = checked || indeterminate ? !!indeterminate : dash
  if (shown !== dash) setDash(shown)
  return (
    <CheckboxPrimitive
      checked={indeterminate ? "indeterminate" : checked}
      className={cn(
        "peer relative flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-input transition-colors outline-none group-has-disabled/field:opacity-50 group-has-[:focus-visible]/field-label:ring-0 group-has-[:focus-visible]/field-label:not-data-checked:border-input after:absolute after:-inset-x-3 after:-inset-y-2 cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 aria-invalid:aria-checked:border-primary dark:bg-input/30 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground data-indeterminate:border-primary data-indeterminate:bg-primary data-indeterminate:text-primary-foreground group-has-[:focus-visible]/field-label:data-checked:border-primary dark:data-checked:bg-primary dark:data-indeterminate:border-primary dark:data-indeterminate:bg-primary",
        className
      )}
      {...props}
    >
      <CheckboxIndicator
        className="grid place-content-center text-current transition-[scale,opacity] duration-150 ease-emphasized data-starting-style:scale-50 data-starting-style:opacity-0 data-ending-style:scale-50 data-ending-style:opacity-0 [&>svg]:size-3.5"
      >
        {/* A partly selected "select all" shows a dash, not a tick. */}
        {shown ? <MinusIcon /> : <CheckIcon />}
      </CheckboxIndicator>
    </CheckboxPrimitive>
  )
}

export { Checkbox }
