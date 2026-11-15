import { createRouter } from "@tanstack/react-router"
import { APP_BASE } from "@/lib/app-path"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createRouter({
    routeTree,
    basepath: APP_BASE,
    scrollRestoration: true,
    defaultPreload: "intent",
  })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
