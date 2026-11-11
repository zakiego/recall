import * as React from "react"
import { useRouterState } from "@tanstack/react-router"

// A navigation shorter than this never shows the bar, so quick page switches stay still.
const SHOW_AFTER_MS = 150
// How long the finished bar takes to fill and fade before it resets (see .route-progress).
const DONE_MS = 400

type Phase = "idle" | "loading" | "done"

/**
 * A 2px bar on the header's bottom edge while a page switch waits on the
 * server. It answers the gap between a click and the page: the sidebar has
 * already moved, the old page is still there, and before this nothing said
 * the click was heard. It only tracks navigations (the URL changed), not the
 * background refreshes that Add and Connectors run every few seconds.
 */
export function RouteProgress() {
  const navigating = useRouterState({
    select: (s) => s.isLoading && s.location.href !== s.resolvedLocation?.href,
  })
  const [phase, setPhase] = React.useState<Phase>("idle")

  React.useEffect(() => {
    if (navigating) {
      // Restart from empty if the last bar is still fading out.
      setPhase((p) => (p === "done" ? "idle" : p))
      const show = window.setTimeout(() => setPhase("loading"), SHOW_AFTER_MS)
      return () => window.clearTimeout(show)
    }
    setPhase((p) => (p === "loading" ? "done" : "idle"))
  }, [navigating])

  React.useEffect(() => {
    if (phase !== "done") return
    const reset = window.setTimeout(() => setPhase("idle"), DONE_MS)
    return () => window.clearTimeout(reset)
  }, [phase])

  return (
    <>
      <div aria-hidden="true" data-phase={phase} className="route-progress" />
      {/* Always mounted, so a screen reader hears the text change. */}
      <span role="status" className="sr-only">
        {phase === "loading" ? "Loading page…" : ""}
      </span>
    </>
  )
}
