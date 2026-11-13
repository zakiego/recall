import type { Viewport } from "next"
import Link from "next/link"
import "@/components/landing/landing.css"
import { inter } from "@/lib/landing-fonts"
import { RecallMark } from "@/lib/layout.shared"
import { appName } from "@/lib/shared"

// The landing and the legal pages: always dark, after Linear (see
// components/landing/landing.css). The docs keep fumadocs' own chrome.
export const viewport: Viewport = {
  themeColor: "#08090a",
  colorScheme: "dark",
}

const FOOTER_LINKS = [
  { href: "/docs", text: "Docs" },
  { href: "/privacy", text: "Privacy" },
  { href: "/terms", text: "Terms" },
]

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <div className={`${inter.variable} dark lp-root`}>
      <header className="lp-header">
        <div className="lp-header-in">
          <Link href="/" className="lp-brand">
            <RecallMark />
            {appName}
          </Link>
          <nav aria-label="Main" className="lp-navs">
            <Link href="/docs" className="lp-nav">
              Docs
            </Link>
            {/* The dashboard is the app on the same host, not a page of this
                site, so it's a plain link with no client-side routing. */}
            <a href="/app" className="lp-nav">
              Dashboard
            </a>
          </nav>
        </div>
      </header>
      {children}
      <footer className="lp-footer">
        <p>
          <span className="font-mono">recall.zakiego.com</span>
          <span aria-hidden="true"> · </span>
          built by <a href="https://zakiego.com">Zakiego</a>
        </p>
        <nav aria-label="Footer">
          {FOOTER_LINKS.map((l) => (
            <Link key={l.href} href={l.href}>
              {l.text}
            </Link>
          ))}
        </nav>
      </footer>
    </div>
  )
}
