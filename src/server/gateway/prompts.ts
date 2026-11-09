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

Avoid duplicates:
- One fact per idea. Merge closely related details (role, client, dates, terms) into one fact instead of splitting them across several.
- Don't restate something another new fact or an existing memory already says, even in different words.
- Prefer fewer, denser facts over many thin ones.

Conflicts (an extra "conflict" key in the answer):
"conflict":[{"ref":number,"fact":string,"keywords":string,"note":string}]
- Use "conflict" when new content disagrees with an existing memory and it is NOT clear the new one replaces it: two sources state different things, neither says the other is outdated, and you can't tell which is right.
- "fact" is what the new content says, written like any other fact. "note" is one short sentence for a human saying what disagrees ("Email says paid on the last Friday of the month; memory says paid weekly after timesheet approval").
- A conflict keeps both facts and asks a human to decide. Don't also "add", "update" or "invalidate" that memory. When the new content clearly supersedes the old one (it is newer, says things changed, or corrects it), use "update" instead. For an unclear disagreement this rule wins over the "update" rule above.

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
