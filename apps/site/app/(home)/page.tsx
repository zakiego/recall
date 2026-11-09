import { ArrowRightIcon, BookOpenIcon } from "lucide-react"
import type { Metadata } from "next"
import Link from "next/link"
import { MemoryGraph } from "@/components/landing/memory-graph"
import { Button } from "@/components/ui/button"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from "@/components/ui/item"
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
const POINTS: { href: string; title: string; line: string }[] = [
  { href: "/docs/how-it-works", title: "Facts keep their history", line: "A changed fact becomes a new version. The old one keeps its dates." },
  { href: "/docs/mcp", title: "Agents plug in over MCP", line: "Claude Code and other agents read and write through MCP or REST." },
  { href: "/docs/self-hosting", title: "One container, one SQLite file", line: "Self-hosted on your own machine, with no cloud database." },
]


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
    <div className="flex flex-1 flex-col">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />

      <section className="mx-auto flex w-full max-w-6xl flex-col gap-6 px-4 pt-6 pb-16 md:gap-8 md:px-6 lg:gap-12 lg:pt-14 lg:pb-24">
        <div className="hero-in flex flex-col gap-6">
          <h1 className="max-w-[18ch] text-hero">Memory for AI agents that keeps its history</h1>
          <p className="max-w-[52ch] text-lg text-muted-foreground">
            Agents tell Recall what they learn. It keeps every fact current and never forgets what it used to be.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Button size="lg" render={<Link href="/docs" />} nativeButton={false}>
              <BookOpenIcon data-icon="inline-start" />
              Read the docs
            </Button>
            <Button size="lg" variant="ghost" render={<Link href="/docs/how-it-works" />} nativeButton={false}>
              How it works
              <ArrowRightIcon data-icon="inline-end" />
            </Button>
          </div>
        </div>
        <div className="graph-in mx-auto w-full max-w-[60rem]">
          <MemoryGraph />
        </div>
      </section>

      <section aria-label="What Recall does" className="mx-auto w-full max-w-6xl px-4 pb-20 md:px-6 lg:pb-28">
        <ItemGroup className="grid gap-2 border-t pt-6 sm:grid-cols-3 sm:gap-4">
          {POINTS.map((p) => (
            <div key={p.href} role="listitem" className="flex">
              <Item render={<Link href={p.href} />} className="-mx-3 items-start sm:mx-0 sm:-ml-3">
                <ItemContent>
                  <ItemTitle className="line-clamp-none">
                    <h2>{p.title}</h2>
                  </ItemTitle>
                  <ItemDescription className="line-clamp-none">{p.line}</ItemDescription>
                </ItemContent>
                <ItemActions className="pt-0.5">
                  <ArrowRightIcon className="size-4 text-muted-foreground transition-transform duration-150 ease-(--ease-emphasized) group-hover/item:translate-x-0.5" />
                </ItemActions>
              </Item>
            </div>
          ))}
        </ItemGroup>
      </section>
    </div>
  )
}
