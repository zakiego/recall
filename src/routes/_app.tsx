import * as React from "react"
import { createFileRoute, Outlet, redirect, useNavigate, useRouter, useRouterState } from "@tanstack/react-router"
import { LogOutIcon, UsersRoundIcon } from "lucide-react"
import { z } from "zod"

import { AppSidebar } from "@/components/app-sidebar"
import { SiteHeader } from "@/components/site-header"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { authClient } from "@/lib/auth-client"
import { getContainers, getSession, switchWorkspace } from "@/lib/fns"
import { isSettings } from "@/lib/pages"
import { AppError } from "@/components/app-error"
import { useContainer } from "@/hooks/use-container"

export const Route = createFileRoute("/_app")({
  validateSearch: z.object({ c: z.string().trim().min(1).max(128).optional() }),
  beforeLoad: async ({ location }) => {
    const session = await getSession()
    const viewer = session.viewer
    if (!viewer) throw redirect({ to: "/login", search: { next: location.href } })
    return { defaultContainer: session.defaultContainer, viewer }
  },
  loader: async ({ context }) => (context.viewer.workspace ? getContainers() : { containers: [], defaultContainer: context.defaultContainer }),
  // The container list barely changes; don't refetch it on every page switch.
  staleTime: 15_000,
  component: AppLayout,
  errorComponent: AppError,
})

function useSignOut() {
  const navigate = useNavigate()
  const router = useRouter()
  return async () => {
    await authClient.signOut()
    await router.invalidate()
    await navigate({ to: "/login", search: {} })
  }
}

function AppLayout() {
  const { viewer } = Route.useRouteContext()
  if (!viewer.workspace) return <NoWorkspace username={viewer.user.username} />
  return <Shell />
}

function Shell() {
  const container = useContainer()
  const { viewer } = Route.useRouteContext()
  const { containers } = Route.useLoaderData()
  const navigate = useNavigate()
  const router = useRouter()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const signOut = useSignOut()

  const onWorkspaceChange = async (id: string) => {
    await switchWorkspace({ data: { id } })
    // Containers belong to a workspace, so start from the default one.
    await navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, c: undefined }) })
    await router.invalidate()
  }

  return (
    <SidebarProvider
      style={{ "--sidebar-width": "calc(var(--spacing) * 60)", "--header-height": "calc(var(--spacing) * 14)" } as React.CSSProperties}
    >
      <a href="#main-content" className="skip-link">
        Skip to content
      </a>
      <AppSidebar variant="inset" container={container} username={viewer.user.username} onLogout={() => void signOut()} />
      <SidebarInset>
        <SiteHeader
          container={container}
          containers={containers}
          showContainer={!isSettings(pathname)}
          workspace={viewer.workspace!}
          workspaces={viewer.workspaces}
          onWorkspaceChange={onWorkspaceChange}
          onContainerChange={(c) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, c }) })}
        />
        <main id="main-content" tabIndex={-1} data-no-ring="" className="@container/main mx-auto flex w-full max-w-[90rem] flex-1 flex-col gap-6 px-4 pt-6 pb-10 outline-none md:px-6 lg:px-8 lg:pt-8">
          <Outlet />
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}

// Signed in, but every membership is gone (removed, or left the last one).
function NoWorkspace({ username }: { username: string }) {
  const signOut = useSignOut()
  return (
    <div className="flex min-h-svh items-center justify-center bg-background px-4">
      <Empty className="max-w-md">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <UsersRoundIcon />
          </EmptyMedia>
          <EmptyTitle>You're not in a workspace</EmptyTitle>
          <EmptyDescription>
            Signed in as <span className="font-medium text-foreground">{username}</span>. Ask a workspace owner or admin for an invite link, then open
            it here.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" onClick={() => void signOut()}>
            <LogOutIcon data-icon="inline-start" />
            Log out
          </Button>
        </EmptyContent>
      </Empty>
    </div>
  )
}
