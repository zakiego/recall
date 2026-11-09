import * as React from "react"
import { createFileRoute, Outlet, redirect, useNavigate, useRouter, useRouterState } from "@tanstack/react-router"
import { z } from "zod"

import { AppSidebar } from "@/components/app-sidebar"
import { SiteHeader } from "@/components/site-header"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { getContainers, getSession, logout } from "@/lib/fns"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"

export const Route = createFileRoute("/_app")({
  validateSearch: z.object({ c: z.string().trim().min(1).max(128).optional() }),
  beforeLoad: async ({ location }) => {
    const session = await getSession()
    if (!session.authed) throw redirect({ to: "/login", search: { next: location.href } })
    return { defaultContainer: session.defaultContainer }
  },
  loader: () => getContainers(),
  // The container list barely changes; don't refetch it on every page switch.
  staleTime: 15_000,
  component: AppLayout,
  errorComponent: AppError,
})

function AppLayout() {
  const container = useContainer()
  const { containers } = Route.useLoaderData()
  const navigate = useNavigate()
  const router = useRouter()
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  const onLogout = async () => {
    await logout()
    await router.invalidate()
    await navigate({ to: "/login", search: {} })
  }

  return (
    <SidebarProvider
      style={{ "--sidebar-width": "calc(var(--spacing) * 60)", "--header-height": "calc(var(--spacing) * 14)" } as React.CSSProperties}
    >
      <a href="#main-content" className="skip-link">
        Skip to content
      </a>
      <AppSidebar variant="inset" container={container} onLogout={() => void onLogout()} />
      <SidebarInset>
        <SiteHeader
          container={container}
          containers={containers}
          showContainer={pathname !== "/keys"}
          onContainerChange={(c) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, c }) })}
        />
        <main id="main-content" tabIndex={-1} data-no-ring="" className="@container/main mx-auto flex w-full max-w-[90rem] flex-1 flex-col gap-6 px-4 pt-6 pb-10 outline-none md:px-6 lg:px-8 lg:pt-8">
          <Outlet />
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}
