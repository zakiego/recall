import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared"
import { appName } from "./shared"

export function RecallMark() {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className="size-6 shrink-0">
      <rect width="32" height="32" rx="8" className="fill-foreground" />
      <circle cx="16" cy="16" r="6" fill="none" strokeWidth="3" className="stroke-background" />
      <circle cx="16" cy="16" r="1.8" className="fill-background" />
    </svg>
  )
}

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <span className="flex items-center gap-2 font-semibold tracking-tight">
          <RecallMark />
          {appName}
        </span>
      ),
    },
    links: [{ text: "Docs", url: "/docs", active: "nested-url" }],
  }
}
