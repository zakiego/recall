"use client"

import {
  Toast as ToastPrimitive,
  ToastAction as ToastActionPrimitive,
  ToastClose as ToastClosePrimitive,
  ToastDescription as ToastDescriptionPrimitive,
  ToastRegion,
  ToastTitle as ToastTitlePrimitive,
  toast as sdkToast,
  type ToastType,
} from "@ui-sdk/react/toast"
import type * as React from "react"
import { XIcon, CircleCheckIcon, InfoIcon, TriangleAlertIcon, OctagonXIcon, Loader2Icon } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

type ToastOptions = {
  title?: React.ReactNode
  description?: React.ReactNode
  type?: Exclude<ToastType, "default">
  // 0 keeps the toast until it's dismissed.
  timeout?: number
}

const toast = {
  add: ({ timeout, ...options }: ToastOptions) =>
    sdkToast({ ...options, duration: timeout === 0 ? Infinity : timeout }),
}

function Toast({ className, ...props }: Styled<typeof ToastPrimitive>) {
  return (
    <ToastPrimitive
      className={cn(
        "group/toast pointer-events-auto absolute right-0 bottom-0 z-[calc(1000-var(--toast-index))] w-full origin-bottom rounded-xl bg-popover text-popover-foreground shadow-(--shadow-elevated) will-change-transform outline-none",
        "[--gap:0.75rem] [--height:var(--toast-front-height,var(--toast-height))] [--offset-y:calc(var(--toast-offset)*-1+calc(var(--toast-index)*var(--gap)*-1)+var(--toast-swipe-y,0px))] [--peek:0.75rem] [--scale:calc(max(0,1-(var(--toast-index)*0.1)))] [--shrink:calc(1-var(--scale))]",
        "h-(--height) [transform:translateX(var(--toast-swipe-x,0px))_translateY(calc(var(--toast-swipe-y,0px)-(var(--toast-index)*var(--peek))-(var(--shrink)*var(--height))))_scale(var(--scale))] [transition:transform_500ms_cubic-bezier(0.22,1,0.36,1),opacity_500ms,height_150ms]",
        "after:absolute after:top-full after:left-0 after:h-[calc(var(--gap)+1px)] after:w-full after:content-['']",
        "data-expanded:h-(--toast-height) data-expanded:[transform:translateX(var(--toast-swipe-x,0px))_translateY(var(--offset-y))]",
        "data-swiping:[transition:none] data-starting-style:[transform:translateY(150%)]",
        "[&[data-ending-style]:not([data-swipe-direction])]:[transform:translateY(150%)]",
        "data-ending-style:data-[swipe-direction=down]:[transform:translateY(calc(var(--toast-swipe-y,0px)+150%))]",
        "data-ending-style:data-[swipe-direction=left]:[transform:translateX(calc(var(--toast-swipe-x,0px)-150%))_translateY(var(--offset-y))]",
        "data-ending-style:data-[swipe-direction=right]:[transform:translateX(calc(var(--toast-swipe-x,0px)+150%))_translateY(var(--offset-y))]",
        "data-ending-style:data-[swipe-direction=up]:[transform:translateY(calc(var(--toast-swipe-y,0px)-150%))]",
        className
      )}
      {...props}
    />
  )
}

function ToastContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="toast-content"
      className={cn(
        "flex h-full items-center gap-3 overflow-hidden p-4 transition-opacity duration-250 ease-[cubic-bezier(0.22,1,0.36,1)] group-not-data-front/toast:opacity-0 group-data-expanded/toast:opacity-100",
        className
      )}
      {...props}
    />
  )
}

function ToastTitle({ className, ...props }: Styled<typeof ToastTitlePrimitive>) {
  return <ToastTitlePrimitive className={cn("text-sm font-medium", className)} {...props} />
}

function ToastDescription({ className, ...props }: Styled<typeof ToastDescriptionPrimitive>) {
  return <ToastDescriptionPrimitive className={cn("text-sm text-muted-foreground", className)} {...props} />
}

function ToastAction({ className, ...props }: Styled<typeof ToastActionPrimitive>) {
  return (
    <ToastActionPrimitive
      render={<Button variant="outline" size="sm" />}
      className={cn("shrink-0", className)}
      {...props}
    />
  )
}

function ToastClose({ className, children, ...props }: Styled<typeof ToastClosePrimitive>) {
  return (
    <ToastClosePrimitive
      aria-label="Close toast"
      render={<Button variant="ghost" size="icon-sm" />}
      className={cn(
        "relative shrink-0 text-muted-foreground after:absolute after:-inset-2 after:content-[''] hover:text-foreground",
        className
      )}
      {...props}
    >
      {children ?? <XIcon aria-hidden="true" />}
    </ToastClosePrimitive>
  )
}

const ICONS: Partial<Record<ToastType, React.ReactNode>> = {
  success: <CircleCheckIcon className="text-success-fg" aria-hidden="true" />,
  info: <InfoIcon aria-hidden="true" />,
  warning: <TriangleAlertIcon aria-hidden="true" />,
  error: <OctagonXIcon className="text-destructive-fg" aria-hidden="true" />,
  loading: <Loader2Icon className="animate-spin" aria-hidden="true" />,
}

function ToastIcon({ type }: { type: ToastType | undefined }) {
  const icon = type && ICONS[type]
  if (!icon) return null
  return (
    <span
      data-slot="toast-icon"
      className="shrink-0 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4"
    >
      {icon}
    </span>
  )
}

function Toaster({ className, ...props }: Omit<Styled<typeof ToastRegion>, "children">) {
  return (
    <ToastRegion
      side="bottom"
      align="end"
      className={cn(
        "pointer-events-none fixed inset-x-4 bottom-4 z-50 mx-auto w-auto max-w-sm outline-none sm:right-4 sm:left-auto sm:mx-0 sm:w-full",
        className
      )}
      {...props}
    >
      {(item) => (
        <Toast>
          <ToastContent>
            <ToastIcon type={item.type} />
            <div className="flex min-w-0 flex-1 flex-col gap-1">
              <ToastTitle />
              <ToastDescription />
            </div>
            {item.action && <ToastAction />}
            <ToastClose />
          </ToastContent>
        </Toast>
      )}
    </ToastRegion>
  )
}

export { Toaster, Toast, ToastAction, ToastClose, ToastContent, ToastDescription, ToastTitle, toast }
