import type { MetadataRoute } from "next"
import { siteUrl } from "@/lib/shared"

export const dynamic = "force-static"

export default function robots(): MetadataRoute.Robots {
  return {
    // The API lives on the same host but isn't pages. The dashboard (/app) is
    // left crawlable on purpose: every page there sends `noindex`, and a crawler
    // that's blocked from fetching it never sees that and can list the URL anyway.
    rules: [{ userAgent: "*", allow: "/", disallow: ["/v1/", "/mcp", "/api/auth/"] }],
    sitemap: `${siteUrl}/sitemap.xml`,
  }
}
