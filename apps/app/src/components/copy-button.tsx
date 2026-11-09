/**
 * Copy something to the clipboard, and say so three ways: the icon turns into
 * a check, the label reads "Copied", and a polite live region tells a screen
 * reader, which would not re-read a button whose name changed under it.
 *
 * Both labels are stacked in one grid cell so the button keeps the width of
 * the longer one and nothing beside it shifts when the word changes.
 */

import * as React from "react"
import { CheckIcon, CopyIcon } from "lucide-react"

import { IconSwap } from "@/components/icon-swap"
import { Button } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { cn } from "@/lib/utils"

const RESET_MS = 1500

export function CopyButton({
  text,
  label = "Copy",
  copiedLabel = "Copied",
  name,
  variant = "outline",
  size = "sm",
  className,
}: {
  text: string
  label?: string
  copiedLabel?: string
  /** Accessible name when several copy buttons share a page, e.g. "Copy API key". Starts with the visible label. */
  name?: string
  variant?: "outline" | "default" | "ghost" | "secondary"
  size?: "sm" | "default"
  className?: string
}) {
  const [copied, setCopied] = React.useState(false)
  const timer = React.useRef<ReturnType<typeof setTimeout>>(undefined)

  React.useEffect(() => () => clearTimeout(timer.current), [])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), RESET_MS)
    } catch {
      toast.add({
        title: "Copy failed",
        description: "Select the text and copy it by hand.",
        type: "error",
        timeout: 0,
      })
    }
  }

  return (
    <>
      <Button
        type="button"
        variant={variant}
        size={size}
        className={className}
        aria-label={name}
        onClick={() => void copy()}
      >
        <IconSwap
          active={copied}
          from={<CopyIcon />}
          to={<CheckIcon />}
        />
        <span className="inline-grid">
          <span
            className={cn(
              "col-start-1 row-start-1",
              copied && "invisible"
            )}
          >
            {label}
          </span>
          <span
            aria-hidden="true"
            className={cn(
              "col-start-1 row-start-1",
              !copied && "invisible"
            )}
          >
            {copiedLabel}
          </span>
        </span>
      </Button>
      <span role="status" className="sr-only">
        {copied ? "Copied to clipboard" : ""}
      </span>
    </>
  )
}
