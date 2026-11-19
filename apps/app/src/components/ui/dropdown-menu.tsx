import {
  Menu,
  MenuContent,
  MenuGroup,
  MenuGroupLabel,
  MenuItem,
  MenuItemIndicator,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
  MenuTrigger,
} from "@ui-sdk/react/menu"
import type * as React from "react"
import { CheckIcon } from "lucide-react"

import { cn } from "@/lib/utils"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

const DropdownMenu = Menu
const DropdownMenuTrigger = MenuTrigger
const DropdownMenuGroup = MenuGroup
const DropdownMenuRadioGroup = MenuRadioGroup

function DropdownMenuContent({
  align = "start",
  alignOffset = 0,
  side = "bottom",
  sideOffset = 4,
  className,
  ...props
}: Styled<typeof MenuContent>) {
  return (
    <MenuContent
      align={align}
      alignOffset={alignOffset}
      side={side}
      sideOffset={sideOffset}
      className={cn(
        "z-50 max-h-(--available-height) w-(--anchor-width) min-w-32 origin-(--transform-origin) overflow-x-hidden overflow-y-auto overscroll-contain rounded-xl bg-popover p-1 text-popover-foreground shadow-(--shadow-elevated) duration-150 ease-emphasized outline-none data-closed:duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:overflow-hidden data-closed:fade-out-0 data-closed:zoom-out-95",
        className
      )}
      {...props}
    />
  )
}

function DropdownMenuLabel({
  className,
  inset,
  ...props
}: Styled<typeof MenuGroupLabel> & { inset?: boolean }) {
  return (
    <MenuGroupLabel
      data-inset={inset || undefined}
      className={cn("px-2 py-1 text-xs font-medium text-muted-foreground data-inset:ps-7", className)}
      {...props}
    />
  )
}

function DropdownMenuItem({
  className,
  inset,
  variant = "default",
  ...props
}: Styled<typeof MenuItem> & {
  inset?: boolean
  variant?: "default" | "destructive"
}) {
  return (
    <MenuItem
      data-inset={inset || undefined}
      data-variant={variant}
      destructive={variant === "destructive"}
      className={cn(
        "group/dropdown-menu-item relative flex cursor-pointer items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm outline-hidden pointer-coarse:py-2.5 select-none focus:bg-accent focus:text-accent-foreground not-data-[variant=destructive]:focus:**:text-accent-foreground data-inset:ps-7 data-[variant=destructive]:text-destructive data-[variant=destructive]:focus:bg-destructive/10 data-[variant=destructive]:focus:text-destructive dark:data-[variant=destructive]:focus:bg-destructive/20 data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 data-[variant=destructive]:*:[svg]:text-destructive",
        className
      )}
      {...props}
    />
  )
}

function DropdownMenuRadioItem({
  className,
  children,
  inset,
  ...props
}: Omit<Styled<typeof MenuRadioItem>, "children"> & { inset?: boolean; children?: React.ReactNode }) {
  return (
    <MenuRadioItem
      data-inset={inset || undefined}
      className={cn(
        "relative flex cursor-pointer items-center gap-1.5 rounded-lg py-1.5 pe-8 ps-2 text-sm outline-hidden select-none pointer-coarse:py-2.5 focus:bg-accent focus:text-accent-foreground focus:**:text-accent-foreground data-inset:ps-7 data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className
      )}
      {...props}
    >
      <MenuItemIndicator className="pointer-events-none absolute end-2 flex items-center justify-center">
        <CheckIcon />
      </MenuItemIndicator>
      {children}
    </MenuRadioItem>
  )
}

function DropdownMenuSeparator({ className, ...props }: Styled<typeof MenuSeparator>) {
  return <MenuSeparator className={cn("-mx-1 my-1 h-px bg-border", className)} {...props} />
}

export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
}
