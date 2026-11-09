import type { Metadata, Viewport } from "next"
import { GeistMono } from "geist/font/mono"
import { GeistSans } from "geist/font/sans"
import { Provider } from "@/components/provider"
import { appName, siteDescription, siteImage, siteUrl } from "@/lib/shared"
import "./global.css"

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: { default: `${appName}: a memory engine for AI agents`, template: `%s · ${appName}` },
  description: siteDescription,
  applicationName: appName,
  authors: [{ name: "Zaki", url: "https://zakiego.com" }],
  openGraph: { type: "website", siteName: appName, locale: "en_US", images: [siteImage] },
  twitter: { card: "summary_large_image", images: [siteImage] },
}

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#fafafa" },
    { media: "(prefers-color-scheme: dark)", color: "#0f0f11" },
  ],
}

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${GeistSans.variable} ${GeistMono.variable}`} suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <Provider>{children}</Provider>
      </body>
    </html>
  )
}
