// Turns a GET /v1/profile response into the short block SessionStart injects.
import { clipSentences, truncate } from "./common.mjs"

const OPEN = (container) => `<recall-memory container="${container}">`
const CLOSE = "</recall-memory>"
const INTRO = "What Recall remembers about the user. Background only: the user's current instructions win, and facts can be out of date."

// Search hits worth showing: facts that matched the project name as a keyword.
// Vector-only hits for a bare repo name are mostly noise.
export function relatedFacts(profile, limit = 4) {
  const seen = new Set()
  const out = []
  for (const r of Array.isArray(profile?.searchResults) ? profile.searchResults : []) {
    if (r?.type !== "memory" || r.ftsRank == null || typeof r.text !== "string") continue
    const text = r.text.replace(/\s+/g, " ").trim()
    if (!text || seen.has(text)) continue
    seen.add(text)
    out.push(text)
    if (out.length >= limit) break
  }
  return out
}

// Returns "" when there's nothing to say.
export function buildContext(profile, { project, container, maxChars = 1500 }) {
  const about = clipSentences(profile?.static ?? "", 420)
  const lately = clipSentences(profile?.dynamic ?? "", 520)
  const facts = relatedFacts(profile)
  if (!about && !lately && !facts.length) return ""

  const head = [OPEN(container), INTRO]
  if (about) head.push(`About: ${about}`)
  if (lately) head.push(`Lately: ${lately}`)
  let text = head.join("\n")
  const budget = maxChars - CLOSE.length - 1
  if (facts.length) {
    const title = `\nRelated to ${project}:`
    if (text.length + title.length + 10 < budget) {
      text += title
      for (const f of facts) {
        const line = `\n- ${truncate(f, 220)}`
        if (text.length + line.length > budget) break
        text += line
      }
      if (text.endsWith(title)) text = text.slice(0, -title.length)
    }
  }
  if (text.length > budget) text = truncate(text, budget)
  return `${text}\n${CLOSE}`
}
