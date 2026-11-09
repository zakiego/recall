import { ogImage } from "@/lib/og"

// Exported as out/og/terms.png, so Pages serves it with an image/png content type.
export const dynamic = "force-static"

export function GET() {
  return ogImage({
    kicker: "Terms",
    title: "Terms of service",
    description: "The short rules for using Recall, a personal service that runs by invite only.",
  })
}
