import { ogImage } from "@/lib/og"

// Exported as out/og/privacy.png, so Pages serves it with an image/png content type.
export const dynamic = "force-static"

export function GET() {
  return ogImage({
    kicker: "Privacy",
    title: "Privacy policy",
    description: "What Recall keeps, where it lives, how Gmail data is handled, and how to delete it.",
  })
}
