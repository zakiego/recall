import { useRouteContext, useSearch } from "@tanstack/react-router"

// The container every page is scoped to: ?c= in the URL, else the server default.
export function useContainer() {
  const { c } = useSearch({ from: "/_app" })
  const { defaultContainer } = useRouteContext({ from: "/_app" })
  return c ?? defaultContainer
}
