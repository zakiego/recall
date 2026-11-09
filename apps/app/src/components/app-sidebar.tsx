// The sidebar: the Recall pages, the workspace's settings, then your account.
// Recall links keep the current container (?c=) so switching pages never
// loses where you are.
import { Link, useRouterState } from "@tanstack/react-router"
import { LogOutIcon } from "lucide-react"

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { isSettings, PAGES } from "@/lib/pages"

export function RecallMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <circle cx="16" cy="16" r="8" fill="none" stroke="currentColor" strokeWidth="3" />
      <circle cx="16" cy="16" r="2.5" fill="currentColor" />
    </svg>
  )
}

const GROUPS = ["Recall", "Workspace", "You"] as const

export function AppSidebar({
  container,
  username,
  onLogout,
  ...props
}: { container: string; username: string; onLogout: () => void } & React.ComponentProps<typeof Sidebar>) {
  const { setOpenMobile } = useSidebar()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const close = () => setOpenMobile(false)

  return (
    <Sidebar collapsible="offcanvas" {...props}>
      <SidebarHeader className="px-3 pt-4 pb-2">
        <Link to="/" search={{ c: container }} onClick={close} className="flex items-center gap-2.5 rounded-lg px-1 py-1">
          <span
            aria-hidden="true"
            className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-[inset_0_1px_0_oklch(1_0_0/0.15)]"
          >
            <RecallMark className="size-5" />
          </span>
          <span className="flex flex-col leading-tight">
            <span className="font-heading text-sm font-semibold text-foreground">Recall</span>
            <span className="text-xs text-muted-foreground">Memory engine</span>
          </span>
        </Link>
      </SidebarHeader>

      <SidebarContent className="gap-1 px-1">
        <nav aria-label="Pages" className="contents">
          {GROUPS.map((group) => (
            <SidebarGroup key={group} role="group" aria-labelledby={`nav-${group}`}>
              <SidebarGroupLabel id={`nav-${group}`}>{group}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu className="gap-0.5">
                  {PAGES.filter((p) => p.group === group).map(({ to, label, Icon }) => {
                    const active = to === "/" ? pathname === "/" : pathname.startsWith(to)
                    return (
                      <SidebarMenuItem key={to}>
                        <SidebarMenuButton
                          isActive={active}
                          render={<Link to={to} search={isSettings(to) ? {} : { c: container }} aria-current={active ? "page" : undefined} onClick={close} />}
                        >
                          <Icon aria-hidden="true" />
                          <span>{label}</span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          ))}
        </nav>
      </SidebarContent>

      <SidebarFooter className="px-3 pb-4">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton onClick={onLogout} aria-label={`Log out ${username}`}>
              <LogOutIcon aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate">Log out</span>
              <span className="max-w-[45%] truncate text-caption text-muted-foreground" title={username}>
                {username}
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
