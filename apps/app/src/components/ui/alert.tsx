import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react"

import { cn } from "@/lib/utils"

// Tinted by role, and always carrying an icon and a title, so the tone is
// never the only thing that says "this failed". Only the destructive tone is
// announced as an alert: a standing "copy it now" note is not an interruption.
const alertVariants = cva(
  "group/alert relative grid w-full grid-cols-[minmax(0,1fr)] gap-0.5 rounded-lg border px-3 py-2.5 text-start text-sm has-data-[slot=alert-action]:relative has-data-[slot=alert-action]:pe-24 has-[>svg]:grid-cols-[auto_minmax(0,1fr)] has-[>svg]:gap-x-2.5 *:[svg]:row-span-2 *:[svg]:translate-y-0.5 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "border-transparent bg-card text-card-foreground shadow-(--shadow-border)",
        destructive:
          "border-destructive-border bg-destructive-bg text-destructive-fg *:data-[slot=alert-description]:text-foreground/80",
        warning:
          "border-warning-border bg-warning-bg text-warning-fg *:data-[slot=alert-description]:text-foreground",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

const DEFAULT_ICON = {
  destructive: <CircleAlertIcon aria-hidden="true" />,
  warning: <TriangleAlertIcon aria-hidden="true" />,
}

function Alert({
  className,
  variant,
  children,
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  const first = React.Children.toArray(children)[0]
  const hasIcon =
    React.isValidElement(first) &&
    (first.type === "svg" || (typeof first.type !== "string" && ![AlertTitle, AlertDescription, AlertAction].includes(first.type as never)))
  return (
    <div
      data-slot="alert"
      role={variant === "destructive" ? "alert" : undefined}
      className={cn(alertVariants({ variant }), className)}
      {...props}
    >
      {!hasIcon && variant && variant !== "default" && DEFAULT_ICON[variant]}
      {children}
    </div>
  )
}

function AlertTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        "font-medium group-has-[>svg]/alert:col-start-2 [&_a]:underline [&_a]:underline-offset-3 [&_a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        "text-sm text-pretty wrap-anywhere text-muted-foreground [&_a]:underline [&_a]:underline-offset-3 [&_a]:hover:text-foreground [&_p:not(:last-child)]:mb-4",
        className
      )}
      {...props}
    />
  )
}

function AlertAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-action"
      className={cn("absolute top-1.5 end-2", className)}
      {...props}
    />
  )
}

export { Alert, AlertTitle, AlertDescription, AlertAction }
