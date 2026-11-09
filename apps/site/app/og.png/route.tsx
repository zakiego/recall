import { ogImage } from "@/lib/og"

// Exported as out/og.png, so Pages serves it with an image/png content type.
export const dynamic = "force-static"

export function GET() {
  return ogImage({
    kicker: "by Zaki",
    title: "Memory for AI agents that keeps its history",
    description: "Self-hosted. Versioned facts over REST, MCP and a dashboard.",
  })
}
