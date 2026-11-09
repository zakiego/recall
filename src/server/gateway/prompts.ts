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
- When new content changes or contradicts an existing memory, put it in "update" with that memory's ref and the new fact. Do not also "add" it.
- When new content says an existing memory is no longer true and there's nothing to replace it with, put its ref in "invalidate".
- Don't add a fact that an existing memory already states. Leave unrelated memories alone.
- Nothing worth remembering: {"add":[],"update":[],"invalidate":[]}.`

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
- "static": stable traits and identity, 1 to 5 sentences.
- "dynamic": current situation, recent preferences, ongoing work and plans, 1 to 5 sentences.
- Plain English, third person. Use only the facts given. Empty string when there's nothing for a section.`

export function profilePrompt(facts: { fact: string; kind: string }[]) {
  return `FACTS:\n${facts.map((f) => `- [${f.kind}] ${f.fact}`).join("\n")}`
}
