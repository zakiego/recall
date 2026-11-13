import {
  Tabs as TabsPrimitive,
  TabsList as TabsListPrimitive,
  TabsPanel,
  TabsTab,
} from "@ui-sdk/react/tabs"
import type * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

function Tabs({
  className,
  orientation = "horizontal",
  ...props
}: Omit<React.ComponentProps<typeof TabsPrimitive>, "className"> & { className?: string }) {
  return (
    <TabsPrimitive
      orientation={orientation}
      className={cn(
        "group/tabs flex gap-2 data-horizontal:flex-col",
        className
      )}
      {...props}
    />
  )
}

const tabsListVariants = cva(
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground group-data-horizontal/tabs:h-8 pointer-coarse:group-data-horizontal/tabs:h-10 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none",
  {
    variants: {
      variant: {
        default: "bg-muted",
        line: "gap-1 bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function TabsList({
  className,
  variant = "default",
  ...props
}: Omit<React.ComponentProps<typeof TabsListPrimitive>, "className"> & { className?: string } & VariantProps<typeof tabsListVariants>) {
  return (
    <TabsListPrimitive
      data-variant={variant}
      className={cn(tabsListVariants({ variant }), className)}
      {...props}
    />
  )
}

// A segmented control. The list is `rounded-lg` with a 3px inset, so the
// trigger takes 10px minus 3px and the two curves stay concentric.
function TabsTrigger({
  className,
  ...props
}: Omit<React.ComponentProps<typeof TabsTab>, "className"> & { className?: string }) {
  return (
    <TabsTab
      className={cn(
        "relative inline-flex h-full flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-[calc(var(--radius-lg)-3px)] px-2.5 text-[0.8125rem] font-medium whitespace-nowrap text-muted-foreground transition-[background-color,color,box-shadow] duration-150 outline-none group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        "data-selected:bg-card data-selected:text-foreground data-selected:shadow-(--shadow-border)",
        className
      )}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: Omit<React.ComponentProps<typeof TabsPanel>, "className"> & { className?: string }) {
  return (
    <TabsPanel
      className={cn("flex-1 text-sm", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, tabsListVariants }
