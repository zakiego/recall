import { HeadContent, Outlet, Scripts, createRootRoute } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { ThemeProvider } from "@/components/theme-provider"
import { Toaster } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import appCss from "@/styles/app.css?url"
import { APP_BASE } from "@/lib/app-path"

// Set the theme class before first paint so there's no light/dark flash, and
// store the browser's time zone so the server knows the viewer's "today".
const THEME_SCRIPT = `(function(){try{
var t=localStorage.getItem('recall.theme')||'system';
var d=t==='dark'||(t!=='light'&&matchMedia('(prefers-color-scheme: dark)').matches);
document.documentElement.classList.add(d?'dark':'light');
document.documentElement.style.colorScheme=d?'dark':'light';
}catch(e){}})();
(function(){try{
var z=Intl.DateTimeFormat().resolvedOptions().timeZone;
if(z)document.cookie='recall.tz='+encodeURIComponent(z)+';path=/;max-age=31536000;samesite=lax';
}catch(e){}})();`

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1, viewport-fit=cover" },
      { title: "Recall" },
      { name: "description", content: "Recall memory engine dashboard." },
      { name: "robots", content: "noindex, nofollow" },
      { name: "theme-color", content: "#fafafa", media: "(prefers-color-scheme: light)" },
      { name: "theme-color", content: "#09090b", media: "(prefers-color-scheme: dark)" },
    ],
    links: [
      { rel: "stylesheet", href: appCss },
      { rel: "icon", type: "image/svg+xml", href: `${APP_BASE}/favicon.svg` },
    ],
  }),
  shellComponent: RootDocument,
  component: () => <Outlet />,
})

function RootDocument({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_SCRIPT }} />
        <HeadContent />
      </head>
      <body>
        <ThemeProvider>
          <TooltipProvider>
            {children}
            <Toaster />
          </TooltipProvider>
        </ThemeProvider>
        <Scripts />
      </body>
    </html>
  )
}
