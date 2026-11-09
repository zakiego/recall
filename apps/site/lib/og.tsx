import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { ImageResponse } from "next/og"

// Social cards, 1200 x 630, in the site's own tokens: zinc ink on paper, Geist
// for words, Geist Mono for the domain. Satori can't read CSS variables, so the
// values below are the light-mode zinc ramp from app/global.css.
const INK = "#18181b" // zinc-9 / zinc-12
const PAPER = "#fafafa" // zinc-1
const MUTED = "#6b6b74" // zinc-11
const RULE = "#e4e4e7" // zinc-6

export const ogSize = { width: 1200, height: 630 }

async function font(file: string) {
  return readFile(join(process.cwd(), "node_modules/geist/dist/fonts", file))
}

export async function ogImage({ title, description, kicker }: { title: string; description: string; kicker: string }) {
  const [semibold, regular, mono] = await Promise.all([
    font("geist-sans/Geist-SemiBold.ttf"),
    font("geist-sans/Geist-Regular.ttf"),
    font("geist-mono/GeistMono-Regular.ttf"),
  ])
  return new ImageResponse(
    <div style={{ display: "flex", flexDirection: "column", width: "100%", height: "100%", padding: 72, background: PAPER, color: INK, fontFamily: "Geist" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 20 }}>
        <svg width="64" height="64" viewBox="0 0 32 32">
          <rect width="32" height="32" rx="8" fill={INK} />
          <circle cx="16" cy="16" r="6" fill="none" stroke={PAPER} strokeWidth="3" />
          <circle cx="16" cy="16" r="1.8" fill={PAPER} />
        </svg>
        <div style={{ display: "flex", fontSize: 40, fontWeight: 600, letterSpacing: "-0.02em" }}>Recall</div>
        <div style={{ display: "flex", fontSize: 32, color: MUTED, marginLeft: 8 }}>{kicker}</div>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 24, marginTop: "auto" }}>
        <div style={{ display: "flex", fontSize: title.length > 40 ? 64 : 76, fontWeight: 600, letterSpacing: "-0.03em", lineHeight: 1.05, maxWidth: 1000 }}>
          {title}
        </div>
        <div style={{ display: "flex", fontSize: 34, color: MUTED, lineHeight: 1.35, maxWidth: 980 }}>{description}</div>
      </div>
      <div style={{ display: "flex", marginTop: 48, paddingTop: 28, borderTop: `2px solid ${RULE}`, fontFamily: "Geist Mono", fontSize: 30, color: MUTED }}>
        recall.zakiego.com
      </div>
    </div>,
    {
      ...ogSize,
      fonts: [
        { name: "Geist", data: semibold, weight: 600, style: "normal" },
        { name: "Geist", data: regular, weight: 400, style: "normal" },
        { name: "Geist Mono", data: mono, weight: 400, style: "normal" },
      ],
    },
  )
}
