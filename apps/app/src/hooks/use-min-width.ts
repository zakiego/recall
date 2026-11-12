import * as React from "react"

// Whether the viewport is at least `px` wide. The server and hydration render
// assume a wide screen, then the client corrects it.
export function useMinWidth(px: number) {
  const query = `(min-width: ${px}px)`
  return React.useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia(query)
      mql.addEventListener("change", onChange)
      return () => mql.removeEventListener("change", onChange)
    },
    () => window.matchMedia(query).matches,
    () => true,
  )
}
