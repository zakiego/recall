import { ArrowRightIcon } from "lucide-react"
import type { JSX } from "react"
import type { Metadata } from "next"
import Link from "next/link"
import { Fragment } from "react"
import { MemoryGraph } from "@/components/landing/memory-graph"
import { HistoryArt, HostArt, McpArt } from "@/components/landing/point-art"
import { RecallMark } from "@/lib/layout.shared"
import { appName, siteDescription, siteImage, siteUrl } from "@/lib/shared"

const homeTitle = `${appName}: a memory engine for AI agents`

export const metadata: Metadata = {
  title: { absolute: homeTitle },
  alternates: { canonical: "/" },
  // Page-level openGraph replaces the layout's, so it's complete here.
  openGraph: { type: "website", siteName: appName, locale: "en_US", url: "/", title: homeTitle, description: siteDescription, images: [siteImage] },
  twitter: { card: "summary_large_image", title: homeTitle, description: siteDescription, images: [siteImage] },
}

// Everything deeper lives in the docs. Each point links to the page that
// explains it instead of explaining it here.
const POINTS: { href: string; title: string; line: string; art: () => JSX.Element }[] = [
  { href: "/docs/how-it-works", title: "Facts keep their history", line: "A changed fact becomes a new version. The old one keeps its dates.", art: HistoryArt },
  { href: "/docs/mcp", title: "Agents plug in over MCP", line: "Claude Code and other agents read and write through MCP or REST.", art: McpArt },
  { href: "/docs/self-hosting", title: "One container, one SQLite file", line: "Self-hosted on your own machine, with no cloud database.", art: HostArt },
]

const HEADLINE = "Memory for AI agents that keeps its history".split(" ")

const jsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: appName,
  description: siteDescription,
  url: siteUrl,
  applicationCategory: "DeveloperApplication",
  operatingSystem: "Linux (Docker)",
  author: { "@type": "Person", name: "Zaki", url: "https://zakiego.com" },
}

export default function HomePage() {
  return (
    <main className="lp-main">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />

      <section className="lp-wrap lp-hero">
        <div className="lp-intro">
          <h1 className="lp-h1">
            {HEADLINE.map((word, i) => (
              <Fragment key={i}>
                {i > 0 ? " " : null}
                <span className="lp-word" style={{ animationDelay: `${i * 40}ms` }}>
                  {word}
                </span>
              </Fragment>
            ))}
          </h1>
          <div className="lp-intro-side">
            <p className="lp-lede">Agents save what they learn, and every fact keeps its old versions</p>
            <div className="lp-cta-row">
              <Link href="/docs" className="lp-cta">
                Read the docs
              </Link>
              <Link href="/docs/how-it-works" className="lp-cta lp-cta-ghost">
                How it works
              </Link>
            </div>
          </div>
        </div>
        <div className="lp-stagebox">
          <div className="lp-stage-bar" aria-hidden="true">
            <span>Fig 0.1 · Memory graph</span>
            <span className="lp-live">
              <span className="lp-live-dot" />
              Demo
            </span>
          </div>
          <div className="lp-stage">
            <span className="lp-cross" data-at="tl" aria-hidden="true" />
            <span className="lp-cross" data-at="tr" aria-hidden="true" />
            <span className="lp-cross" data-at="bl" aria-hidden="true" />
            <span className="lp-cross" data-at="br" aria-hidden="true" />
            <MemoryGraph />
          </div>
        </div>
      </section>

      <hr className="lp-rule" />

      <section className="lp-wrap lp-section" aria-labelledby="does">
        <h2 id="does" className="lp-h2">
          One memory <span className="lp-h2-dim">for all your agents</span>
        </h2>
        <ul className="lp-bento">
          {POINTS.map(({ href, title, line, art: Art }) => (
            <li key={href}>
              <Link href={href} className="lp-card">
                <Art />
                <div>
                  <h3 className="lp-card-title">
                    {title}
                    <ArrowRightIcon aria-hidden="true" className="lp-card-arrow size-4" />
                  </h3>
                  <p className="lp-card-text">{line}</p>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      </section>

      <hr className="lp-rule" />

      <section className="lp-wrap lp-close" aria-labelledby="close">
        <svg className="lp-close-rings" viewBox="-400 -400 800 800" aria-hidden="true">
          {[120, 190, 260, 330, 400].map((r, i) => (
            <circle key={r} r={r} fill="none" strokeWidth={1} strokeDasharray={i === 2 ? "3 9" : undefined} className={i === 2 ? "lp-ring-dash" : undefined} />
          ))}
        </svg>
        <span className="lp-close-mark" aria-hidden="true">
          <RecallMark />
        </span>
        <h2 id="close" className="lp-h2">
          Give your agents a memory
        </h2>
        <div className="lp-cta-row">
          <Link href="/docs" className="lp-cta lp-cta-big">
            Read the docs
          </Link>
          <Link href="/docs/self-hosting" className="lp-cta lp-cta-ghost lp-cta-big">
            Host it yourself
          </Link>
        </div>
      </section>

      <hr className="lp-rule" />
    </main>
  )
}
