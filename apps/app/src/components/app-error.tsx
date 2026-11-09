import * as React from "react"
import { useNavigate, useRouter, type ErrorComponentProps } from "@tanstack/react-router"
import { RotateCwIcon } from "lucide-react"

import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { errorMessage } from "@/lib/format"

// Route error boundary. An expired session goes to login; anything else shows
// what failed with a retry, inside the shell.
export function AppError({ error, reset }: ErrorComponentProps) {
  const router = useRouter()
  const navigate = useNavigate()
  const expired = errorMessage(error).includes("UNAUTHORIZED")

  React.useEffect(() => {
    if (expired) void navigate({ to: "/login", search: {} })
  }, [expired, navigate])

  if (expired) return null
  return (
    <div className="mx-auto w-full max-w-xl p-6">
      <Alert variant="destructive">
        <AlertTitle>This page couldn't load</AlertTitle>
        <AlertDescription>{errorMessage(error)}</AlertDescription>
        <AlertAction>
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              reset()
              void router.invalidate()
            }}
          >
            <RotateCwIcon data-icon="inline-start" />
            Retry
          </Button>
        </AlertAction>
      </Alert>
    </div>
  )
}
