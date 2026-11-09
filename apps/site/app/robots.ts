import type { MetadataRoute } from "next"
import { siteUrl } from "@/lib/shared"

export const dynamic = "force-static"

export default function robots(): MetadataRoute.Robots {
  return {
    // The dashboard and the API live on the same host but aren't pages to index.
    rules: [{ userAgent: "*", allow: "/", disallow: ["/app/", "/v1/", "/mcp", "/api/auth/"] }],
    sitemap: `${siteUrl}/sitemap.xml`,
  }
}
