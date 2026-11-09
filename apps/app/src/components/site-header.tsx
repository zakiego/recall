// Sticky header: where you are, which workspace and container every page is
// showing, and the theme. Translucent so the sidebar toggle and container stay in reach on
// a long list.
import { useRouterState } from "@tanstack/react-router"

import { ContainerSwitcher, type ContainerInfo } from "@/components/container-switcher"
import { ThemeToggle } from "@/components/theme-toggle"
import { WorkspaceSwitcher, type WorkspaceInfo } from "@/components/workspace-switcher"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { PAGES } from "@/lib/pages"

export function SiteHeader({
  container,
  containers,
  onContainerChange,
  showContainer,
  workspace,
  workspaces,
  onWorkspaceChange,
}: {
  container: string
  containers: ContainerInfo[]
  onContainerChange: (c: string) => void
  showContainer: boolean
  workspace: WorkspaceInfo
  workspaces: WorkspaceInfo[]
  onWorkspaceChange: (id: string) => Promise<void>
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const page = PAGES.find((p) => (p.to === "/" ? pathname === "/" : pathname.startsWith(p.to)))

  return (
    <header className="sticky top-0 z-20 flex h-(--header-height) shrink-0 items-center border-b bg-background/85 backdrop-blur-md md:rounded-t-2xl">
      <div className="mx-auto flex w-full max-w-[90rem] items-center gap-2 px-4 md:px-6 lg:px-8">
        <SidebarTrigger className="-ms-1.5" />
        {/* The page heading says the same thing, so a phone gives this room to the switchers. */}
        <Separator orientation="vertical" className="mx-1 h-4 data-vertical:self-auto max-sm:hidden" />
        <span className="min-w-0 truncate text-sm font-medium max-sm:hidden">{page?.label ?? "Recall"}</span>
        <div className="ms-auto flex min-w-0 items-center gap-1.5">
          <WorkspaceSwitcher value={workspace} workspaces={workspaces} onChange={onWorkspaceChange} />
          {showContainer && <ContainerSwitcher value={container} containers={containers} onChange={onContainerChange} />}
          <ThemeToggle />
        </div>
      </div>
    </header>
  )
}
