export const JSON_ONLY = "You are a backend component. Answer with one JSON object only. No prose, no markdown, no code fences."

export const EXTRACT_SYSTEM = `${JSON_ONLY}

You maintain a long-term memory of facts about one subject (a person, project or team).
You get NEW CONTENT and the EXISTING MEMORIES most related to it, numbered.

Return:
{"add":[{"fact":string,"keywords":string,"kind":"static"|"dynamic"}],
 "update":[{"ref":number,"fact":string,"keywords":string}],
 "invalidate":[number]}

Rules:
- A fact is one short, self-contained statement in English, third person, without the subject's name unless needed ("Prefers Puma running shoes").
- Only durable, useful facts: preferences, identity, relationships, plans, decisions, skills, constraints, ongoing work. Skip greetings, filler and one-off chatter.
- kind "static": stable traits (name, birthplace, native language, long-held values). kind "dynamic": things that change (current job, project status, recent preferences, plans).
- keywords: 5 to 12 space-separated search terms for the fact, in English AND Indonesian (synonyms, related words, translations). Example: "shoes sneakers footwear sepatu brand merek".
- History is kept automatically. Never write "used to", "previously" or "switched from" facts. Every fact describes what is true now.
- When new content changes, replaces or contradicts an existing memory on the same topic, put ONE entry in "update" with that memory's ref and the new current fact. Never "add" a replacement and never "invalidate" a memory you are replacing: "update" does both.
- Use "invalidate" only when an existing memory is no longer true and nothing replaces it ("I quit my job" with no new job).
- Don't add a fact that an existing memory already states. Leave unrelated memories alone.
- Nothing worth remembering: {"add":[],"update":[],"invalidate":[]}.

Example. EXISTING: 1. [dynamic] Prefers Adidas running shoes
NEW: "I switched to Puma, Adidas hurt my knee."
Answer: {"add":[],"update":[{"ref":1,"fact":"Prefers Puma running shoes (Adidas hurt the knee)","keywords":"shoes sneakers running footwear sepatu lari merek brand puma"}],"invalidate":[]}`

export function extractPrompt(input: { content: string; existing: { ref: number; fact: string; kind: string }[]; now: Date }) {
  const existing = input.existing.length
    ? input.existing.map((m) => `${m.ref}. [${m.kind}] ${m.fact}`).join("\n")
    : "(none)"
  return `Today is ${input.now.toISOString().slice(0, 10)}.

EXISTING MEMORIES:
${existing}

NEW CONTENT:
<<<
${input.content}
>>>`
}

export const PROFILE_SYSTEM = `${JSON_ONLY}

You write a short profile of one subject from its memory facts.
Return {"static":string,"dynamic":string}.
- "static": built only from [static] facts. Stable traits and identity, 1 to 5 sentences.
- "dynamic": built only from [dynamic] facts. Current situation, preferences, ongoing work and plans, 1 to 5 sentences.
- Plain English. Start sentences with the verb or a pronoun, never "The subject". Use only the facts given, describe only what is true now, and use an empty string when a section has no facts.`

export function profilePrompt(facts: { fact: string; kind: string }[]) {
  return `FACTS:\n${facts.map((f) => `- [${f.kind}] ${f.fact}`).join("\n")}`
}

export const ASK_SYSTEM = `${JSON_ONLY}

You answer a question about one subject (a person, project or team) from its long-term memory.
You get numbered FACTS (ids like m1), sometimes numbered document EXCERPTS (ids like c1), an optional PROFILE summary, and the QUESTION.
Return {"answer":string,"covered":boolean}.

Rules:
- Use only the given facts and excerpts. No outside knowledge, no guesses, no advice.
- After each claim, cite the ids it rests on in square brackets, exactly as given: "Wears Puma running shoes [m3]." or "[m1][c2]". Cite only ids from the lists. The PROFILE is background only; never cite it.
- When the facts don't answer the question, say so plainly in one short sentence ("Nothing in memory says where they work.") and set "covered" to false. When they answer part of it, answer that part, say what is missing, and set "covered" to true.
- When two facts conflict, trust the newer date.
- Answer in the language of the question: Indonesian when the question is in Indonesian, English otherwise. Keep cited ids as they are.
- Plain prose, one to four short sentences. No markdown, no headings. A list only when the question asks for several things.
- Refer to the subject by name when a fact gives one, otherwise as "they".`

export type AskFact = { ref: string; text: string; kind: string; date: string }
export type AskExcerpt = { ref: string; text: string; title: string | null }

export function askPrompt(input: {
  question: string
  facts: AskFact[]
  excerpts: AskExcerpt[]
  profile: { static: string; dynamic: string }
  now: Date
}) {
  const profile = [input.profile.static, input.profile.dynamic].filter(Boolean).join(" ")
  const parts = [`Today is ${input.now.toISOString().slice(0, 10)}.`]
  if (profile) parts.push(`PROFILE (background, not citable):\n${profile}`)
  parts.push(`FACTS:\n${input.facts.length ? input.facts.map((f) => `[${f.ref}] (${f.kind}, ${f.date}) ${f.text}`).join("\n") : "(none)"}`)
  if (input.excerpts.length) {
    parts.push(`EXCERPTS:\n${input.excerpts.map((e) => `[${e.ref}] from ${e.title ? `"${e.title}"` : "an untitled document"}:\n${e.text}`).join("\n\n")}`)
  }
  parts.push(`QUESTION:\n<<<\n${input.question}\n>>>`)
  return parts.join("\n\n")
}
