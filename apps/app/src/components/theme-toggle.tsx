/**
 * Flip between light and dark. The theme starts on the system setting; the
 * first press pins the opposite of whatever is showing, same as the `D` key.
 *
 * The accessible name says where the button goes, not where you are, because
 * that is what pressing it does.
 */

import { MoonIcon, SunIcon } from "lucide-react"
import * as React from "react"

import { IconSwap } from "@/components/icon-swap"
import { useTheme } from "@/components/theme-provider"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const noSubscribe = () => () => {}

export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, setTheme } = useTheme()
  // The tooltip is in the server HTML, where the theme isn't known yet, so its
  // text waits for hydration instead of failing it.
  const hydrated = React.useSyncExternalStore(noSubscribe, () => true, () => false)
  const dark = resolvedTheme === "dark"
  const label = dark ? "Switch to light theme" : "Switch to dark theme"

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            className={className}
            aria-label={label}
            onClick={() => setTheme(dark ? "light" : "dark")}
          />
        }
      >
        <IconSwap active={dark} from={<MoonIcon />} to={<SunIcon />} />
      </TooltipTrigger>
      <TooltipContent>
        {hydrated && label}
        <Kbd>D</Kbd>
      </TooltipContent>
    </Tooltip>
  )
}
