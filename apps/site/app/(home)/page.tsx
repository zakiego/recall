import { ArrowRightIcon, BookOpenIcon } from "lucide-react"
import type { Metadata } from "next"
import Link from "next/link"
import { HeroExchange } from "@/components/landing/hero-exchange"
import { Pipeline } from "@/components/landing/pipeline"
import { Screens } from "@/components/landing/screens"
import { Button } from "@/components/ui/button"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemTitle } from "@/components/ui/item"
import { Separator } from "@/components/ui/separator"
import { appName, siteDescription, siteImage, siteUrl } from "@/lib/shared"

const homeTitle = `${appName}: a memory engine for AI agents`

export const metadata: Metadata = {
  title: { absolute: homeTitle },
  alternates: { canonical: "/" },
  // Page-level openGraph replaces the layout's, so it's complete here.
  openGraph: { type: "website", siteName: appName, locale: "en_US", url: "/", title: homeTitle, description: siteDescription, images: [siteImage] },
  twitter: { card: "summary_large_image", title: homeTitle, description: siteDescription, images: [siteImage] },
}

const STACK: { area: string; choice: string }[] = [
  { area: "App", choice: "TanStack Start on Nitro, React 19, shadcn/ui" },
  { area: "Storage", choice: "One SQLite file: better-sqlite3, FTS5, sqlite-vec" },
  { area: "Embeddings", choice: "transformers.js with multilingual-e5-small, in-process" },
  { area: "Extraction", choice: "Claude Haiku through my own AI gateway" },
  { area: "Agents", choice: "REST under /v1 and an MCP server over streamable HTTP" },
  { area: "Auth", choice: "Better Auth: usernames, passkeys, workspaces, invite links" },
  { area: "Hosting", choice: "Docker on a Mac mini behind a Cloudflare Tunnel; this site on Cloudflare Pages" },
]

const DECISIONS: { title: string; body: string }[] = [
  {
    title: "History instead of overwrite",
    body: "When a fact changes, Recall writes a new version and gives the old one an end date. When an agent says something stale, you can see what it believed and which document changed it.",
  },
  {
    title: "Search never calls an LLM",
    body: "Keyword and vector search run inside SQLite and the embedder runs in the same process, so search is local and fast. Only extraction, profiles and Ask call an LLM.",
  },
  {
    title: "One file, one job at a time",
    body: "Facts, chunks and both indexes live in one SQLite file, so a backup is one online snapshot. LLM work runs one job at a time through a gateway shared with my other projects, and questions go to the front of the queue.",
  },
  {
    title: "Secrets stay out",
    body: "API keys, tokens and card numbers are masked before text is stored or sent to a model. ID numbers, passports, IBANs and passwords never become facts.",
  },
]

const DOCS: { href: string; title: string; line: string }[] = [
  { href: "/docs/how-it-works", title: "How it works", line: "The pipeline from a request to a versioned fact." },
  { href: "/docs/api", title: "REST API", line: "Every /v1 route with requests and responses." },
  { href: "/docs/mcp", title: "MCP", line: "Tools for Claude Code and other agents." },
  { href: "/docs/self-hosting", title: "Self-hosting", line: "Architecture, backups and the deploy model." },
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

      <section className="mx-auto grid w-full max-w-6xl gap-12 px-4 pt-14 pb-20 md:px-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,32rem)] lg:gap-16 lg:pt-24 lg:pb-28">
        <div className="hero-in flex flex-col gap-6 lg:pt-6">
          <h1 className="max-w-[15ch] text-4xl font-semibold tracking-[-0.03em] text-balance md:text-5xl lg:text-[3.5rem] lg:leading-[1.05]">
            Memory for AI agents that keeps its history
          </h1>
          <div className="flex max-w-[60ch] flex-col gap-4 text-lg text-muted-foreground">
            <p>
              Recall is a memory engine I built and host myself. Agents send it chats, notes, links and files. It pulls out short
              facts, keeps them current, and serves them back over a REST API, an MCP server and a dashboard.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button size="lg" render={<Link href="/docs" />} nativeButton={false}>
              <BookOpenIcon data-icon="inline-start" />
              Read the docs
            </Button>
            <Button size="lg" variant="ghost" render={<a href="#how-it-works" />} nativeButton={false}>
              How it works
              <ArrowRightIcon data-icon="inline-end" />
            </Button>
          </div>
        </div>
        <HeroExchange />
      </section>

      <Separator />

      <section id="how-it-works" className="mx-auto flex w-full max-w-6xl scroll-mt-20 flex-col gap-10 px-4 py-20 md:px-6">
        <div className="flex max-w-[62ch] flex-col gap-3">
          <h2 className="text-3xl font-semibold tracking-[-0.025em]">How it works</h2>
          <p className="text-muted-foreground">
            Writing is slow and careful, reading is fast and local. Adding something returns right away; the facts show up a few
            seconds later. Searching never waits on an LLM.
          </p>
        </div>
        <Pipeline />
        <p className="text-sm text-muted-foreground">
          The full walkthrough, with the limits and retry rules, is in{" "}
          <Link href="/docs/how-it-works" className="font-medium text-foreground underline underline-offset-4">
            How it works
          </Link>
          .
        </p>
      </section>

      <Separator />

      <section className="mx-auto flex w-full max-w-6xl flex-col gap-10 px-4 py-20 md:px-6">
        <div className="flex max-w-[62ch] flex-col gap-3">
          <h2 className="text-3xl font-semibold tracking-[-0.025em]">The dashboard</h2>
          <p className="text-muted-foreground">
            For the people behind the agents: check what Recall believes about a person or project, fix a wrong fact, search like an
            agent would, add something by hand, and hand out API keys. Screens below use sample data.
          </p>
        </div>
        <Screens />
      </section>

      <Separator />

      <section className="mx-auto grid w-full max-w-6xl gap-12 px-4 py-20 md:px-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,24rem)] lg:gap-16">
        <div className="flex flex-col gap-8">
          <div className="flex max-w-[62ch] flex-col gap-3">
            <h2 className="text-3xl font-semibold tracking-[-0.025em]">How I built it</h2>
            <p className="text-muted-foreground">
              A few choices shaped everything else. Each one trades something away on purpose.
            </p>
          </div>
          <div className="grid gap-x-10 gap-y-8 sm:grid-cols-2">
            {DECISIONS.map((d) => (
              <div key={d.title} className="flex flex-col gap-2">
                <h3 className="text-heading">{d.title}</h3>
                <p className="text-sm text-muted-foreground">{d.body}</p>
              </div>
            ))}
          </div>
        </div>
        <div className="flex flex-col gap-4">
          <h3 className="text-heading">Stack</h3>
          <dl className="flex flex-col">
            {STACK.map((s, i) => (
              <div key={s.area} className="flex flex-col">
                {i > 0 && <Separator />}
                <div className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-3 py-3 text-sm">
                  <dt className="text-muted-foreground">{s.area}</dt>
                  <dd>{s.choice}</dd>
                </div>
              </div>
            ))}
          </dl>
        </div>
      </section>

      <Separator />

      <section className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-20 md:px-6">
        <div className="flex max-w-[62ch] flex-col gap-3">
          <h2 className="text-3xl font-semibold tracking-[-0.025em]">Read the docs</h2>
          <p className="text-muted-foreground">Written from the code, down to the limits and status codes.</p>
        </div>
        <ItemGroup className="grid gap-3 sm:grid-cols-2">
          {DOCS.map((d) => (
            <Item key={d.href} variant="outline" render={<Link href={d.href} />}>
              <ItemContent>
                <ItemTitle>{d.title}</ItemTitle>
                <ItemDescription>{d.line}</ItemDescription>
              </ItemContent>
              <ItemActions>
                <ArrowRightIcon className="size-4 text-muted-foreground transition-transform duration-150 ease-(--ease-emphasized) group-hover/item:translate-x-0.5" />
              </ItemActions>
            </Item>
          ))}
        </ItemGroup>
      </section>

    </div>
  )
}
