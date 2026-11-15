import { HeadContent, Outlet, createRootRoute } from "@tanstack/react-router"
import { ThemeProvider } from "@/components/theme-provider"
import { Toaster } from "@/components/ui/toast"

// The document (meta tags, the theme and time zone script, the favicon) is
// index.html; the stylesheet is imported in main.tsx. HeadContent keeps each
// route's title.
export const Route = createRootRoute({
  component: () => (
    <ThemeProvider>
      {/* Each route's head(): the page title follows navigation. */}
      <HeadContent />
      <Outlet />
      <Toaster />
    </ThemeProvider>
  ),
})
