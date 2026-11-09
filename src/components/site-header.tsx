// Sticky header: where you are, which container every page is showing, and
// the theme. Translucent so the sidebar toggle and container stay in reach on
// a long list.
import { useRouterState } from "@tanstack/react-router"

import { ContainerSwitcher, type ContainerInfo } from "@/components/container-switcher"
import { ThemeToggle } from "@/components/theme-toggle"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { PAGES } from "@/lib/pages"

export function SiteHeader({
  container,
  containers,
  onContainerChange,
  showContainer,
}: {
  container: string
  containers: ContainerInfo[]
  onContainerChange: (c: string) => void
  showContainer: boolean
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const page = PAGES.find((p) => (p.to === "/" ? pathname === "/" : pathname.startsWith(p.to)))

  return (
    <header className="sticky top-0 z-20 flex h-(--header-height) shrink-0 items-center border-b bg-background/85 backdrop-blur-md md:rounded-t-2xl">
      <div className="mx-auto flex w-full max-w-[90rem] items-center gap-2 px-4 md:px-6 lg:px-8">
        <SidebarTrigger className="-ms-1.5" />
        <Separator orientation="vertical" className="mx-1 h-4 data-vertical:self-auto" />
        <span className="min-w-0 truncate text-sm font-medium">{page?.label ?? "Recall"}</span>
        <div className="ms-auto flex min-w-0 items-center gap-1.5">
          {showContainer && <ContainerSwitcher value={container} containers={containers} onChange={onContainerChange} />}
          <ThemeToggle />
        </div>
      </div>
    </header>
  )
}
