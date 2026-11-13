import {
  Dialog as Sheet,
  DialogBackdrop,
  DialogClose as SheetClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger as SheetTrigger,
} from "@ui-sdk/react/dialog"
import type * as React from "react"
import { XIcon } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function SheetContent({
  className,
  children,
  side = "right",
  showCloseButton = true,
  ...props
}: Omit<Styled<typeof DialogContent>, "children"> & {
  children?: React.ReactNode
  side?: "top" | "right" | "bottom" | "left"
  showCloseButton?: boolean
}) {
  return (
    <>
      <DialogBackdrop
        className="fixed inset-0 z-50 bg-black/20 transition-opacity duration-300 ease-drawer data-ending-style:opacity-0 data-ending-style:duration-200 data-starting-style:opacity-0 dark:bg-black/50"
      />
      <DialogContent
        data-side={side}
        className={cn(
          "fixed z-50 flex flex-col gap-4 overscroll-contain bg-card bg-clip-padding text-sm text-card-foreground shadow-(--shadow-elevated) transition-[opacity,translate] duration-300 ease-drawer data-ending-style:opacity-0 data-ending-style:duration-200 data-starting-style:opacity-0 data-[side=bottom]:inset-x-0 data-[side=bottom]:bottom-0 data-[side=bottom]:h-auto data-[side=bottom]:border-t data-[side=bottom]:data-ending-style:translate-y-[2.5rem] data-[side=bottom]:data-starting-style:translate-y-[2.5rem] data-[side=left]:inset-y-0 data-[side=left]:left-0 data-[side=left]:h-full data-[side=left]:w-3/4 data-[side=left]:data-ending-style:translate-x-[-2.5rem] data-[side=left]:data-starting-style:translate-x-[-2.5rem] data-[side=right]:inset-y-0 data-[side=right]:right-0 data-[side=right]:h-full data-[side=right]:w-3/4 data-[side=right]:data-ending-style:translate-x-[2.5rem] data-[side=right]:data-starting-style:translate-x-[2.5rem] data-[side=top]:inset-x-0 data-[side=top]:top-0 data-[side=top]:h-auto data-[side=top]:border-b data-[side=top]:data-ending-style:translate-y-[-2.5rem] data-[side=top]:data-starting-style:translate-y-[-2.5rem] sm:max-w-sm",
          className
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <SheetClose
            aria-label="Close"
            render={<Button variant="ghost" className="absolute top-3.5 end-3.5" size="icon-sm" />}
          >
            <XIcon aria-hidden="true" />
          </SheetClose>
        )}
      </DialogContent>
    </>
  )
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-1 border-b px-5 py-4 pe-14", className)}
      {...props}
    />
  )
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-footer"
      className={cn("mt-auto flex flex-col gap-3 px-5 py-4", className)}
      {...props}
    />
  )
}

function SheetTitle({ className, ...props }: Styled<typeof DialogTitle>) {
  return <DialogTitle className={cn("font-heading text-heading text-foreground", className)} {...props} />
}

function SheetDescription({ className, ...props }: Styled<typeof DialogDescription>) {
  return (
    <DialogDescription className={cn("text-sm text-pretty text-muted-foreground", className)} {...props} />
  )
}

export {
  Sheet,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
}
