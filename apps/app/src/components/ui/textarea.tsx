import { Textarea as TextareaPrimitive } from "@ui-sdk/react/textarea"
import type * as React from "react"

import { cn } from "@/lib/utils"

function Textarea({
  className,
  ...props
}: Omit<React.ComponentProps<typeof TextareaPrimitive>, "className"> & { className?: string }) {
  return (
    <TextareaPrimitive
      className={cn(
        "flex field-sizing-content min-h-16 w-full rounded-lg border border-input bg-transparent px-2.5 py-2 text-base transition-[border-color,background-color] duration-150 outline-none placeholder:text-muted-foreground hover:border-foreground/40 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
