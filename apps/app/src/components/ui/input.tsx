import { Input as InputPrimitive } from "@ui-sdk/react/input"
import type * as React from "react"

import { cn } from "@/lib/utils"

function Input({
  className,
  ...props
}: Omit<React.ComponentProps<typeof InputPrimitive>, "className"> & { className?: string }) {
  return (
    <InputPrimitive
      className={cn(
        "h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1 text-base transition-[border-color,background-color] duration-150 outline-none pointer-coarse:h-10 file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground hover:border-foreground/40 disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30",
        className
      )}
      {...props}
    />
  )
}

export { Input }
