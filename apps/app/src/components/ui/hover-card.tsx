import {
  HoverCard,
  HoverCardContent as HoverCardContentPrimitive,
  HoverCardTrigger,
} from "@ui-sdk/react/hover-card"
import type * as React from "react"

import { cn } from "@/lib/utils"

function HoverCardContent({
  className,
  side = "bottom",
  sideOffset = 4,
  align = "center",
  alignOffset = 4,
  ...props
}: Omit<React.ComponentProps<typeof HoverCardContentPrimitive>, "className"> & { className?: string }) {
  return (
    <HoverCardContentPrimitive
      align={align}
      alignOffset={alignOffset}
      side={side}
      sideOffset={sideOffset}
      className={cn(
        "z-50 w-64 origin-(--transform-origin) rounded-xl bg-popover p-2.5 text-sm text-popover-foreground shadow-(--shadow-elevated) outline-hidden duration-150 ease-emphasized data-closed:duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
        className
      )}
      {...props}
    />
  )
}

export { HoverCard, HoverCardTrigger, HoverCardContent }
