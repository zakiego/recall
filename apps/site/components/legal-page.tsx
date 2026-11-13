import type { Metadata } from "next"
import type { ReactNode } from "react"
import { appName, contactEmail, legalEffectiveDate } from "@/lib/shared"

// The privacy policy and terms: one readable column with the docs' prose styles,
// under the landing page's header and footer.

export function legalMetadata({ path, title, description }: { path: string; title: string; description: string }): Metadata {
  // A shared link shows only og:title, so it carries the product name the
  // <title> template adds.
  const shareTitle = `${title} · ${appName}`
  const image = { url: `/og${path}.png`, width: 1200, height: 630, alt: `${appName}: ${title}` }
  return {
    title,
    description,
    alternates: { canonical: path },
    // Page-level openGraph replaces the layout's, so it's complete here.
    openGraph: { type: "article", siteName: appName, locale: "en_US", url: path, title: shareTitle, description, images: [image] },
    twitter: { card: "summary_large_image", title: shareTitle, description, images: [image] },
  }
}

export function LegalPage({ title, summary, children }: { title: string; summary: string; children: ReactNode }) {
  return (
    <main className="lp-main">
      <article className="lp-wrap pt-10 pb-20 lg:pt-16 lg:pb-28">
        <div className="max-w-[68ch]">
          <header className="flex flex-col gap-3 border-b pb-6">
            <h1 className="text-[1.75em] font-[590] tracking-[-0.012em] text-balance">{title}</h1>
            <p className="text-lg text-muted-foreground text-pretty">{summary}</p>
            <p className="text-sm text-muted-foreground">
              Effective <time dateTime={legalEffectiveDate} className="font-mono tabular-nums">{legalEffectiveDate}</time>
            </p>
          </header>
          <div className="prose mt-8 [&_:is(h2,h3)]:scroll-mt-20">{children}</div>
        </div>
      </article>
    </main>
  )
}

// The contact address, or a marked placeholder until it's set in lib/shared.ts.
export function ContactEmail() {
  if (contactEmail) return <a href={`mailto:${contactEmail}`}>{contactEmail}</a>
  return <mark className="rounded-sm bg-amber-200 px-1 text-amber-950">[contact email to be added]</mark>
}
