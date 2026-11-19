import * as React from "react"
import { useNavigate, useRouter, type ErrorComponentProps } from "@tanstack/react-router"

import { ErrorAlert } from "@/components/error-alert"
import { errorMessage, explainError } from "@/lib/format"

// Route error boundary. An expired session goes to login; anything else shows
// what failed, with a retry when one can help, inside the shell.
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
      <ErrorAlert
        title="This page couldn't load"
        error={explainError(error, "This page", "Something went wrong on the server.")}
        onRetry={() => {
          reset()
          void router.invalidate()
        }}
      />
    </div>
  )
}
