import {
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogClose,
  AlertDialogContent as AlertDialogContentPrimitive,
  AlertDialogDescription as AlertDialogDescriptionPrimitive,
  AlertDialogTitle as AlertDialogTitlePrimitive,
  AlertDialogTrigger,
} from "@ui-sdk/react/alert-dialog"
import type * as React from "react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function AlertDialogContent({
  className,
  size = "default",
  ...props
}: Styled<typeof AlertDialogContentPrimitive> & {
  size?: "default" | "sm"
}) {
  return (
    <>
      <AlertDialogBackdrop
        className="fixed inset-0 isolate z-50 bg-black/50 duration-200 data-closed:duration-150 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0"
      />
      <AlertDialogContentPrimitive
        data-size={size}
        className={cn(
          "group/alert-dialog-content fixed top-1/2 left-1/2 z-50 grid max-h-[calc(100dvh-2rem)] w-full -translate-x-1/2 overflow-y-auto overscroll-contain -translate-y-1/2 gap-4 rounded-xl bg-popover p-5 text-popover-foreground shadow-(--shadow-elevated) duration-200 ease-emphasized outline-none data-closed:duration-150 has-data-[slot=alert-dialog-footer]:pb-0 data-[size=default]:max-w-xs data-[size=sm]:max-w-xs data-[size=default]:sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        {...props}
      />
    </>
  )
}

function AlertDialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-header"
      className={cn(
        "grid grid-rows-[auto_1fr] place-items-center gap-1.5 text-center has-data-[slot=alert-dialog-media]:grid-rows-[auto_auto_1fr] has-data-[slot=alert-dialog-media]:gap-x-4 sm:group-data-[size=default]/alert-dialog-content:place-items-start sm:group-data-[size=default]/alert-dialog-content:text-start sm:group-data-[size=default]/alert-dialog-content:has-data-[slot=alert-dialog-media]:grid-rows-[auto_1fr]",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-footer"
      className={cn(
        "sticky bottom-0 z-10 -mx-5 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-[color-mix(in_oklab,var(--color-muted)_50%,var(--color-popover))] dark:bg-[color-mix(in_oklab,var(--color-background)_50%,var(--color-popover))] px-5 py-4 group-data-[size=sm]/alert-dialog-content:grid group-data-[size=sm]/alert-dialog-content:grid-cols-2 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogMedia({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-media"
      className={cn(
        "mb-2 inline-flex size-10 items-center justify-center rounded-md bg-muted sm:group-data-[size=default]/alert-dialog-content:row-span-2 *:[svg:not([class*='size-'])]:size-6",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogTitle({ className, ...props }: Styled<typeof AlertDialogTitlePrimitive>) {
  return (
    <AlertDialogTitlePrimitive
      className={cn(
        "font-heading text-heading sm:group-data-[size=default]/alert-dialog-content:group-has-data-[slot=alert-dialog-media]/alert-dialog-content:col-start-2",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogDescription({ className, ...props }: Styled<typeof AlertDialogDescriptionPrimitive>) {
  return (
    <AlertDialogDescriptionPrimitive
      className={cn(
        "text-sm text-balance text-muted-foreground md:text-pretty *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

// The confirming answer stays a Button the page closes itself, so a failed
// request keeps the dialog open with its error.
const AlertDialogAction = Button

function AlertDialogCancel({
  variant = "outline",
  size = "default",
  ...props
}: Omit<React.ComponentProps<typeof AlertDialogClose>, "className"> & { className?: string } &
  Pick<React.ComponentProps<typeof Button>, "variant" | "size">) {
  return <AlertDialogClose render={<Button variant={variant} size={size} />} {...props} />
}

export {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
  AlertDialogTrigger,
}
