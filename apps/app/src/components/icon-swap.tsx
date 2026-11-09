/**
 * Two icons in one slot, cross-faded when the state flips: copy to check, sun
 * to moon.
 *
 * Both stay mounted and one sits absolutely on top of the other, so the swap
 * animates in both directions with plain CSS transitions and no motion
 * library. The values (scale 0.25, blur 4px, 300ms on the `emphasized`
 * curve) are the house recipe for a contextual icon; the label next to the icon
 * changes too, so the state never depends on seeing the motion.
 */

import * as React from "react"

import { cn } from "@/lib/utils"

const BASE =
  "grid place-items-center transition-[opacity,filter,scale] duration-300 ease-emphasized"
const SHOWN = "scale-100 opacity-100 blur-[0px]"
const HIDDEN = "scale-[0.25] opacity-0 blur-[4px]"

export function IconSwap({
  active,
  from,
  to,
  className,
}: {
  active: boolean
  from: React.ReactNode
  to: React.ReactNode
  className?: string
}) {
  return (
    <span
      aria-hidden="true"
      className={cn("relative inline-grid place-items-center", className)}
    >
      <span className={cn(BASE, "absolute inset-0", active ? SHOWN : HIDDEN)}>
        {to}
      </span>
      <span className={cn(BASE, active ? HIDDEN : SHOWN)}>{from}</span>
    </span>
  )
}
