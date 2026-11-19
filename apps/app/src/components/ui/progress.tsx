import {
  Progress as ProgressPrimitive,
  ProgressIndicator as ProgressIndicatorPrimitive,
  ProgressLabel as ProgressLabelPrimitive,
  ProgressTrack as ProgressTrackPrimitive,
  ProgressValue as ProgressValuePrimitive,
} from "@ui-sdk/react/progress"
import type * as React from "react"

import { cn } from "@/lib/utils"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function Progress({
  className,
  children,
  ...props
}: Omit<Styled<typeof ProgressPrimitive>, "children"> & { children?: React.ReactNode }) {
  return (
    <ProgressPrimitive className={cn("flex flex-wrap gap-3", className)} {...props}>
      {children}
      <ProgressTrack>
        <ProgressIndicator />
      </ProgressTrack>
    </ProgressPrimitive>
  )
}

function ProgressTrack({ className, ...props }: Styled<typeof ProgressTrackPrimitive>) {
  return (
    <ProgressTrackPrimitive
      className={cn(
        "relative flex h-1 w-full items-center overflow-x-hidden rounded-full bg-muted",
        className
      )}
      {...props}
    />
  )
}

function ProgressIndicator({ className, ...props }: Styled<typeof ProgressIndicatorPrimitive>) {
  return (
    <ProgressIndicatorPrimitive
      className={cn("h-full bg-primary transition-[width] duration-200 ease-(--ease-emphasized)", className)}
      {...props}
    />
  )
}

function ProgressLabel({ className, ...props }: Styled<typeof ProgressLabelPrimitive>) {
  return (
    <ProgressLabelPrimitive className={cn("text-sm font-medium", className)} {...props} />
  )
}

function ProgressValue({ className, ...props }: Styled<typeof ProgressValuePrimitive>) {
  return (
    <ProgressValuePrimitive
      className={cn("ms-auto text-sm text-muted-foreground tabular-nums", className)}
      {...props}
    />
  )
}

export { Progress, ProgressTrack, ProgressIndicator, ProgressLabel, ProgressValue }
