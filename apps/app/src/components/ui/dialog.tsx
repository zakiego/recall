"use client"

import {
  Dialog,
  DialogBackdrop,
  DialogClose,
  DialogContent as DialogContentPrimitive,
  DialogDescription as DialogDescriptionPrimitive,
  DialogTitle as DialogTitlePrimitive,
  DialogTrigger,
} from "@ui-sdk/react/dialog"
import type * as React from "react"
import { XIcon } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function DialogContent({
  className,
  children,
  showCloseButton = true,
  ...props
}: Omit<Styled<typeof DialogContentPrimitive>, "children"> & {
  children?: React.ReactNode
  showCloseButton?: boolean
}) {
  return (
    <>
      <DialogBackdrop
        className="fixed inset-0 isolate z-50 bg-black/50 duration-200 data-closed:duration-150 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0"
      />
      <DialogContentPrimitive
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-5 overflow-x-hidden overflow-y-auto overscroll-contain rounded-xl bg-popover p-5 has-data-[slot=dialog-footer]:pb-0 text-sm text-popover-foreground shadow-(--shadow-elevated) duration-200 ease-emphasized outline-none data-closed:duration-150 sm:max-w-md data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <DialogClose
            aria-label="Close"
            render={<Button variant="ghost" className="absolute top-3 end-3" size="icon-sm" />}
          >
            <XIcon aria-hidden="true" />
          </DialogClose>
        )}
      </DialogContentPrimitive>
    </>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "sticky bottom-0 z-10 -mx-5 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-[color-mix(in_oklab,var(--color-muted)_50%,var(--color-popover))] dark:bg-[color-mix(in_oklab,var(--color-background)_50%,var(--color-popover))] px-5 py-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && <DialogClose render={<Button variant="outline" />}>Close</DialogClose>}
    </div>
  )
}

function DialogTitle({ className, ...props }: Styled<typeof DialogTitlePrimitive>) {
  return <DialogTitlePrimitive className={cn("font-heading text-heading pe-8", className)} {...props} />
}

function DialogDescription({ className, ...props }: Styled<typeof DialogDescriptionPrimitive>) {
  return (
    <DialogDescriptionPrimitive
      className={cn(
        "text-sm text-pretty text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
}
