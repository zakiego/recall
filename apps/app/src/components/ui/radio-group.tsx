"use client"

import {
  Radio as RadioPrimitive,
  RadioGroup as RadioGroupPrimitive,
  RadioIndicator,
} from "@ui-sdk/react/radio"
import type * as React from "react"
import { cn } from "@/lib/utils"

function RadioGroup({
  className,
  ...props
}: Omit<React.ComponentProps<typeof RadioGroupPrimitive>, "className"> & { className?: string }) {
  return (
    <RadioGroupPrimitive
      className={cn("grid w-full gap-2", className)}
      {...props}
    />
  )
}

function RadioGroupItem({
  className,
  ...props
}: Omit<React.ComponentProps<typeof RadioPrimitive>, "className" | "children"> & { className?: string }) {
  return (
    <RadioPrimitive
      className={cn(
        "group/radio-group-item peer relative flex aspect-square size-4 shrink-0 rounded-full border border-input bg-transparent outline-none after:absolute after:-inset-x-3 after:-inset-y-2 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 aria-invalid:aria-checked:border-primary dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground dark:data-checked:bg-primary",
        className
      )}
      {...props}
    >
      <RadioIndicator
        className="flex size-4 items-center justify-center"
      >
        <span className="absolute top-1/2 left-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-foreground" />
      </RadioIndicator>
    </RadioPrimitive>
  )
}

export { RadioGroup, RadioGroupItem }
