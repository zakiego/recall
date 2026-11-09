import { createGetUrl } from "fumadocs-core/source"

export const appName = "Recall"
export const siteUrl = "https://recall.zakiego.com"
export const siteDescription =
  "A self-hosted memory engine for AI agents. It turns chats, text, URLs and files into versioned facts and serves them through a REST API, an MCP server and a dashboard."
export const siteImage = {
  url: "/og.png",
  width: 1200,
  height: 630,
  alt: "Recall: memory for AI agents that keeps its history",
}
export const docsRoute = "/docs"
export const docsImageRoute = "/og/docs"
export const docsContentRoute = "/llms.mdx/docs"

const getContentUrl = createGetUrl(docsContentRoute)

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [...page.slugs, "content.md"]
  return { segments, url: getContentUrl(segments, page.locale) }
}

const getImageUrl = createGetUrl(docsImageRoute)

export function getPageImageUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [...page.slugs, "image.png"]
  return { segments, url: getImageUrl(segments, page.locale) }
}

// The privacy policy and terms. Both pages and their OG images read these.
export const legalEffectiveDate = "2026-11-10"
// TODO(zaki): set the address people write to about their data, e.g.
// "you@example.com". While it's null the pages show a visible placeholder.
export const contactEmail: string | null = null
