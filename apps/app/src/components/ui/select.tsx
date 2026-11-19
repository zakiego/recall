"use client"

import {
  Select,
  SelectContent as SelectContentPrimitive,
  SelectGroup as SelectGroupPrimitive,
  SelectGroupLabel,
  SelectItem as SelectItemPrimitive,
  SelectItemIndicator,
  SelectItemText,
  SelectSeparator as SelectSeparatorPrimitive,
  SelectTrigger as SelectTriggerPrimitive,
  SelectValue as SelectValuePrimitive,
} from "@ui-sdk/react/select"
import type * as React from "react"
import { ChevronDownIcon, CheckIcon } from "lucide-react"

import { cn } from "@/lib/utils"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function SelectGroup({ className, ...props }: Styled<typeof SelectGroupPrimitive>) {
  return <SelectGroupPrimitive className={cn("scroll-my-1 p-1", className)} {...props} />
}

function SelectValue({ className, ...props }: Styled<typeof SelectValuePrimitive>) {
  return <SelectValuePrimitive className={cn("flex flex-1 text-start", className)} {...props} />
}

function SelectTrigger({
  className,
  size = "default",
  children,
  ...props
}: Omit<Styled<typeof SelectTriggerPrimitive>, "children"> & {
  children?: React.ReactNode
  size?: "sm" | "default"
}) {
  return (
    <SelectTriggerPrimitive
      data-size={size}
      className={cn(
        "flex w-fit cursor-pointer items-center justify-between gap-1.5 rounded-lg border border-input bg-transparent py-2 ps-2.5 pe-2 text-sm whitespace-nowrap transition-[border-color,background-color] duration-150 outline-none select-none hover:border-foreground/40 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive data-placeholder:text-muted-foreground data-[size=default]:h-8 data-[size=default]:pointer-coarse:h-10 data-[size=sm]:h-7 data-[size=sm]:rounded-md *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-1.5 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 dark:bg-input/30",
        className
      )}
      {...props}
    >
      {children}
      <ChevronDownIcon aria-hidden="true" className="pointer-events-none size-4 text-muted-foreground" />
    </SelectTriggerPrimitive>
  )
}

// The list renders in place, so a parent's child selectors, like Field's
// `*:w-full`, reach it. The important width keeps it at the trigger's.
function SelectContent({
  className,
  side = "bottom",
  sideOffset = 4,
  align = "center",
  alignOffset = 0,
  alignItemWithTrigger = true,
  ...props
}: Styled<typeof SelectContentPrimitive>) {
  return (
    <SelectContentPrimitive
      side={side}
      sideOffset={sideOffset}
      align={align}
      alignOffset={alignOffset}
      alignItemWithTrigger={alignItemWithTrigger}
      data-align-trigger={alignItemWithTrigger}
      className={cn(
        "relative isolate z-50 max-h-[min(var(--available-height),24rem)] w-(--anchor-width)! min-w-36 origin-(--transform-origin) overflow-x-hidden overflow-y-auto overscroll-contain rounded-xl bg-popover text-popover-foreground shadow-(--shadow-elevated) duration-150 ease-emphasized data-closed:duration-100 data-[align-trigger=true]:animate-none data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
        className
      )}
      {...props}
    />
  )
}

function SelectLabel({ className, ...props }: Styled<typeof SelectGroupLabel>) {
  return (
    <SelectGroupLabel className={cn("px-1.5 py-1 text-xs text-muted-foreground", className)} {...props} />
  )
}

function SelectItem({
  className,
  children,
  ...props
}: Omit<Styled<typeof SelectItemPrimitive>, "children"> & { children?: React.ReactNode }) {
  return (
    <SelectItemPrimitive
      className={cn(
        "relative flex w-full cursor-pointer items-center gap-1.5 rounded-lg py-1.5 pe-8 ps-2 text-sm pointer-coarse:py-2.5 outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-highlighted:**:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 *:[span]:last:flex *:[span]:last:items-center *:[span]:last:gap-2",
        className
      )}
      {...props}
    >
      <SelectItemText className="flex flex-1 shrink-0 gap-2 whitespace-nowrap">{children}</SelectItemText>
      <SelectItemIndicator className="pointer-events-none absolute end-2 flex size-4 items-center justify-center">
        <CheckIcon className="pointer-events-none" />
      </SelectItemIndicator>
    </SelectItemPrimitive>
  )
}

function SelectSeparator({ className, ...props }: Styled<typeof SelectSeparatorPrimitive>) {
  return (
    <SelectSeparatorPrimitive
      className={cn("pointer-events-none -mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  )
}

export {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
}
