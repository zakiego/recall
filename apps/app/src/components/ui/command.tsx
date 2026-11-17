import {
  Command as CommandPrimitive,
  CommandEmpty as CommandEmptyPrimitive,
  CommandGroup as CommandGroupPrimitive,
  CommandGroupLabel,
  CommandInput as CommandInputPrimitive,
  CommandItem as CommandItemPrimitive,
  CommandList as CommandListPrimitive,
} from "@ui-sdk/react/command"
import type * as React from "react"
import { CheckIcon, SearchIcon } from "lucide-react"

import { cn } from "@/lib/utils"
import { InputGroup, InputGroupAddon } from "@/components/ui/input-group"

type Styled<T extends React.ElementType> = Omit<React.ComponentProps<T>, "className"> & { className?: string }

function Command({ className, ...props }: Styled<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      className={cn(
        "flex size-full flex-col overflow-hidden rounded-xl! bg-popover p-1 text-popover-foreground",
        className
      )}
      {...props}
    />
  )
}

function CommandInput({ className, ...props }: Styled<typeof CommandInputPrimitive>) {
  return (
    <div data-slot="command-input-wrapper" className="p-1 pb-0">
      <InputGroup className="h-8! rounded-lg! border-input/30 bg-input/30 shadow-none! *:data-[slot=input-group-addon]:pl-2!">
        <CommandInputPrimitive
          // The group draws the ring around the icon and field together.
          data-focus-within=""
          className={cn(
            "w-full text-base outline-hidden disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
            className
          )}
          {...props}
        />
        <InputGroupAddon>
          <SearchIcon aria-hidden="true" className="size-4 shrink-0 opacity-50" />
        </InputGroupAddon>
      </InputGroup>
    </div>
  )
}

function CommandList({ className, ...props }: Styled<typeof CommandListPrimitive>) {
  return (
    <CommandListPrimitive
      className={cn(
        "no-scrollbar max-h-72 scroll-py-1 overflow-x-hidden overflow-y-auto outline-none",
        className
      )}
      {...props}
    />
  )
}

function CommandEmpty({ className, ...props }: Styled<typeof CommandEmptyPrimitive>) {
  // It stays in the page as a status region, so it collapses instead of hiding.
  return <CommandEmptyPrimitive className={cn("py-6 text-center text-sm not-data-empty:py-0", className)} {...props} />
}

function CommandGroup({
  className,
  heading,
  children,
  ...props
}: Omit<Styled<typeof CommandGroupPrimitive>, "children"> & {
  heading?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <CommandGroupPrimitive className={cn("overflow-hidden p-1 text-foreground", className)} {...props}>
      {heading && (
        <CommandGroupLabel className="px-2 py-1.5 text-xs font-medium text-muted-foreground">{heading}</CommandGroupLabel>
      )}
      {children}
    </CommandGroupPrimitive>
  )
}

function CommandItem({
  className,
  children,
  ...props
}: Omit<Styled<typeof CommandItemPrimitive>, "children"> & { children?: React.ReactNode }) {
  return (
    <CommandItemPrimitive
      className={cn(
        "group/command-item relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none in-data-[slot=dialog-content]:rounded-lg! data-disabled:pointer-events-none data-disabled:opacity-50 data-highlighted:bg-muted data-highlighted:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 data-highlighted:*:[svg]:text-foreground",
        className
      )}
      {...props}
    >
      {children}
      <CheckIcon aria-hidden="true" className="ml-auto opacity-0" />
    </CommandItemPrimitive>
  )
}

export { Command, CommandInput, CommandList, CommandEmpty, CommandGroup, CommandItem }
