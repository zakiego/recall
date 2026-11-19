import { CircleAlertIcon, RotateCwIcon } from "lucide-react"

import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import type { ExplainedError } from "@/lib/format"

export function ErrorAlert({
  title,
  error,
  onRetry,
  className,
}: {
  title: string
  error: ExplainedError
  onRetry?: () => void
  className?: string
}) {
  const retry = error.retry && onRetry
  return (
    <Alert variant="destructive" className={className}>
      <CircleAlertIcon />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="flex flex-col gap-1.5">
        <div>{error.message}</div>
        {error.detail && (
          <details>
            <summary className="w-fit cursor-pointer text-caption select-none">Details</summary>
            <p className="mt-1 font-mono text-caption whitespace-pre-wrap wrap-anywhere">{error.detail}</p>
          </details>
        )}
      </AlertDescription>
      {retry && (
        <AlertAction>
          <Button size="sm" variant="outline" onClick={onRetry}>
            <RotateCwIcon data-icon="inline-start" />
            Try again
          </Button>
        </AlertAction>
      )}
    </Alert>
  )
}
