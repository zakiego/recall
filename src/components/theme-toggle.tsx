/**
 * Flip between light and dark. The theme starts on the system setting; the
 * first press pins the opposite of whatever is showing, same as the `D` key.
 *
 * The accessible name says where the button goes, not where you are, because
 * that is what pressing it does.
 */

import { MoonIcon, SunIcon } from "lucide-react"

import { IconSwap } from "@/components/icon-swap"
import { useTheme } from "@/components/theme-provider"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, setTheme } = useTheme()
  const dark = resolvedTheme === "dark"
  const label = dark ? "Switch to light theme" : "Switch to dark theme"

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon"
            className={className}
            aria-label={label}
            onClick={() => setTheme(dark ? "light" : "dark")}
          />
        }
      >
        <IconSwap active={dark} from={<SunIcon />} to={<MoonIcon />} />
      </TooltipTrigger>
      <TooltipContent>
        {label}
        <kbd className="rounded-sm bg-background/20 px-1 font-mono text-[0.6875rem]">
          D
        </kbd>
      </TooltipContent>
    </Tooltip>
  )
}
