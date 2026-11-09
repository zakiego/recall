import { createFileRoute, redirect } from "@tanstack/react-router"

// Keys moved under workspace settings.
export const Route = createFileRoute("/_app/keys")({
  beforeLoad: () => {
    throw redirect({ to: "/settings/keys", replace: true })
  },
})
