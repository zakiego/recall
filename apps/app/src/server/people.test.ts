// People: extraction's "people" block, the resolver, merge, the owner rule,
// tenant isolation and the backfill.
import { beforeEach, describe, expect, it, vi } from "vitest"
import { Route as PeopleRoute } from "@/server/routes-legacy/v1/people"
import { Route as PersonRoute } from "@/server/routes-legacy/v1/people.$id"
import { people as listPeopleApi, person as personApi, search } from "./api"
import { runCli, type CliIo } from "./admin-cli"
import { answerQuestion, type AskDeps } from "./ask"
import { createApiKey } from "./auth"
import { openDb, setDb, type DB } from "./db"
import { jobs, people, personMemories } from "./db/schema"
import type { chatJson } from "./gateway/client"
import { extractWithPeople } from "./gateway/schemas"
import { JobRunner } from "./jobs/runner"
import { enqueue } from "./jobs/queue"
import { addMemoryDirect, editMemory, type MemoryView } from "./memories"
import {
  backfillPeople,
  deletePerson,
  getPerson,
  isContainerOwner,
  isSharedMailbox,
  linkMentions,
  looksLikeOrganization,
  listPeople,
  mergePeople,
  namesLookAlike,
  peopleForMemories,
  peopleNamedIn,
  suggestPeopleMerges,
  updatePerson,
} from "./people"
import { addContent } from "./api"
import { createHandlers } from "./pipeline"

const W = "ws_people"
const C = "zaki"
let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

const scope = { workspaceId: W, container: C }
const mention = (name: string, extra: Partial<{ aliases: string[]; role: string; organization: string; email: string; handle: string; summary: string }> = {}) => ({
  name,
  aliases: [],
  facts: [],
  role: undefined,
  organization: undefined,
  handle: undefined,
  summary: undefined,
  ...extra,
})
const fact = (text: string, ws = W, container = C) => addMemoryDirect(db, { workspaceId: ws, container, fact: text, keywords: "", kind: "dynamic" })

// Plays the model. Profile calls get a fixed profile, everything else the next scripted reply.
function scripted(replies: unknown[]) {
  const systems: string[] = []
  const chat = vi.fn(async (req: { system: string; prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
    systems.push(req.system)
    if (req.system.includes("short profile")) return req.schema.parse({ static: "", dynamic: "" })
    return req.schema.parse(replies.shift() ?? {})
  })
  return { chat: chat as unknown as typeof chatJson, calls: chat, systems }
}

async function drain(runner: JobRunner) {
  for (let i = 0; i < 50; i++) if (!(await runner.runOnce())) return
}

describe("extract people block", () => {
  it("parses people and drops bad entries without failing the answer", () => {
    const r = extractWithPeople.parse({
      add: [{ fact: "Works with Clara on the redesign", keywords: "design" }],
      people: [
        { name: "Clara Simmons", aliases: ["Clara"], role: "Product designer", email: "clara@vorneo.id", facts: ["a1"] },
        { name: "", facts: ["a1"] },
        "not a person",
        { name: "Budi", email: "not-an-email", facts: [1] },
      ],
    })
    expect(r.people).toEqual([
      expect.objectContaining({ name: "Clara Simmons", aliases: ["Clara"], role: "Product designer", email: "clara@vorneo.id", facts: ["a1"] }),
      expect.objectContaining({ name: "Budi", email: undefined, facts: ["1"] }),
    ])
    expect(extractWithPeople.parse({ add: [] }).people).toEqual([])
    expect(extractWithPeople.parse({ add: [], people: null }).people).toEqual([])
  })

  it("links the facts an extract writes to the people it names", async () => {
    const { chat, systems } = scripted([
      {
        add: [
          { fact: "Works with Clara Simmons on the Vorneo listing redesign", keywords: "design vorneo", kind: "dynamic" },
          { fact: "Prefers Puma running shoes", keywords: "shoes", kind: "dynamic" },
        ],
        people: [
          { name: "Clara Simmons", aliases: ["Clara"], role: "Product designer", organization: "Vorneo", handle: "@clara", facts: ["a1"] },
          // The subject is never a contact, even when the model lists them.
          { name: "Zaki", facts: ["a1", "a2"] },
          // A ref to a fact this answer didn't write is ignored.
          { name: "Ghost", facts: ["a9"] },
        ],
      },
      {
        update: [{ ref: 1, fact: "Works with Clara on the Vorneo listing redesign and the mobile app", keywords: "design" }],
        people: [{ name: "clara", facts: ["u1"] }],
      },
    ])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date("2026-11-10") }), () => {})
    addContent(db, W, { container: C, content: "Clara Simmons from Vorneo and I are redesigning the listing page. I run in Puma." })
    await drain(runner)
    expect(systems[0]).toContain('"people"')
    expect(systems[0]).toContain('memory named "zaki"')

    const list = listPeople(db, { workspaceId: W, container: C })
    expect(list.people.map((p) => p.name)).toEqual(["Clara Simmons"])
    const clara = list.people[0]!
    expect(clara).toMatchObject({ role: "Product designer", organization: "Vorneo", handles: ["@clara"], aliases: ["Clara"], factCount: 1 })

    // A later update names her by first name only: same person, linked to the new version.
    addContent(db, W, { container: C, content: "Clara and I also took on the mobile app." })
    await drain(runner)
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(1)
    const page = getPerson(db, W, clara.id)!
    expect(page.facts.map((f) => f.fact)).toEqual(["Works with Clara on the Vorneo listing redesign and the mobile app"])
    expect(getPerson(db, W, clara.id, { history: true })!.facts).toHaveLength(2)
  })
})

describe("resolver", () => {
  it("matches exact names, aliases, emails and handles ignoring case and accents", async () => {
    const [a, b, c, d] = await Promise.all([fact("Budi handles payments"), fact("Budi sent the invoice"), fact("Renée reviewed the contract"), fact("Pinged @budi.s on Slack")])
    await linkMentions(db, scope, [{ mention: mention("Budi Santoso", { aliases: ["Budi"], email: "budi@kebunsawit.id" }), memoryIds: [a.id] }])
    await linkMentions(db, scope, [
      { mention: mention("BUDI SANTOSO"), memoryIds: [b.id] },
      { mention: mention("Pak Budi", { email: "Budi@KebunSawit.id", handle: "@budi.s" }), memoryIds: [d.id] },
      { mention: mention("Renee"), memoryIds: [c.id] },
      { mention: mention("renée"), memoryIds: [c.id] },
    ])
    const { people } = listPeople(db, { workspaceId: W, container: C, sort: "name" })
    expect(people.map((p) => [p.name, p.factCount])).toEqual([
      ["Budi Santoso", 3],
      ["Renee", 1],
    ])
    expect(people[0]!.handles).toEqual(["@budi.s"])
    expect(people[0]!.aliases).toContain("Pak Budi")
  })

  it("takes a fuzzy name match only when the embedding agrees", async () => {
    expect(namesLookAlike("Clara", "Clara Simmons")).toBe(true)
    expect(namesLookAlike("Clara S.", "Clara Simmons")).toBe(true)
    expect(namesLookAlike("Budi Santosa", "Budi Santoso")).toBe(true)
    expect(namesLookAlike("Clara Wong", "Clara Simmons")).toBe(false)

    const work = await fact("Works with Clara Simmons, a product designer at Vorneo, on the listing page redesign")
    await linkMentions(db, scope, [
      { mention: mention("Clara Simmons", { role: "Product designer", organization: "Vorneo", summary: "Product designer at Vorneo" }), memoryIds: [work.id] },
    ])
    // Same Clara: a designer reviewing mockups.
    const mockups = await fact("Clara recommended switching the Vorneo listing cards to a two-column grid")
    await linkMentions(db, scope, [{ mention: mention("Clara"), memoryIds: [mockups.id] }])
    // A different Clara: the subject's sister.
    const sister = await fact("Clara is his younger sister and studies medicine in Surabaya")
    await linkMentions(db, scope, [{ mention: mention("Clara", { role: "Sister", summary: "Younger sister studying medicine" }), memoryIds: [sister.id] }])

    const { people } = listPeople(db, { workspaceId: W, container: C, sort: "name" })
    expect(people.map((p) => [p.name, p.factCount])).toEqual([
      ["Clara", 1],
      ["Clara Simmons", 2],
    ])
    expect(peopleForMemories(db, W, [sister.id])[sister.id]!.map((p) => p.name)).toEqual(["Clara"])
  })

  it("keeps numbered names and people with different emails apart", async () => {
    expect(namesLookAlike("Contractor 1", "Contractor 2")).toBe(false)
    for (const i of [1, 2]) {
      const m = await fact(`Contractor ${i} delivered the audit`)
      await linkMentions(db, scope, [{ mention: mention(`Contractor ${i}`), memoryIds: [m.id] }])
    }
    const [x, y] = await Promise.all([fact("Clara sent the invoice"), fact("Clara sent the invoice again")])
    await linkMentions(db, scope, [{ mention: mention("Clara Simmons", { email: "clara@vorneo.id" }), memoryIds: [x.id] }])
    await linkMentions(db, scope, [{ mention: mention("Clara", { email: "clara@gmail.com" }), memoryIds: [y.id] }])
    expect(listPeople(db, { workspaceId: W, container: C, sort: "name" }).people.map((p) => p.name)).toEqual(["Clara", "Clara Simmons", "Contractor 1", "Contractor 2"])
  })

  // Pairs the resolver split in production (Gmail import, 2026-11-10). Real
  // people write from more than one address, so a full name that matches
  // goes to the embedding check even when the email is new.
  const pairs: [string, Parameters<typeof mention>[1], string, string, Parameters<typeof mention>[1], string, string][] = [
    [
      "Rizky Kurniawan",
      { email: "rizky.kurniawan@example.com", role: "Data analyst", organization: "LaporCovid-19", summary: "Data analyst at LaporCovid-19 who sends the weekly case recaps" },
      "Rizky Kurniawan sent the weekly COVID-19 case recap for Jakarta to the LaporCovid-19 team",
      "Rizky Kurn",
      { email: "rizky@wargalapor.example", organization: "LaporCovid-19" },
      "Rizky Kurn sent the LaporCovid-19 case recap for Jakarta and asked for corrections",
      "Rizky Kurniawan",
    ],
    [
      "Sekar Ayu Lestari",
      { email: "sekar.lestari@example.com", role: "Coordinator", organization: "LaporCovid-19", summary: "Coordinates citizen reports at LaporCovid-19" },
      "Sekar Ayu Lestari asked for the March report on vaccine complaints from LaporCovid-19",
      "Sekar Ayu Lestarii",
      { email: "sekar@wargalapor.example", organization: "LaporCovid-19" },
      "Sekar Ayu Lestarii forwarded citizen complaints about vaccines to the LaporCovid-19 team",
      "Sekar Ayu Lestari",
    ],
    [
      "Maya Lee Hartono",
      { email: "maya@foundation.example", role: "Program officer", organization: "Open Data Foundation", summary: "Program officer handling the grant" },
      "Maya Lee Hartono sent the grant agreement for signature",
      "Maya Hartono",
      { email: "maya.hartono@example.com", role: "Program officer" },
      "Maya Hartono scheduled a call to review the quarterly grant report",
      "Maya Lee Hartono",
    ],
    [
      "Dewi Ratna",
      { email: "dewi@kantor.example", role: "Finance staff", summary: "Handles reimbursements and honorarium payments" },
      "Dewi Ratna approved the travel reimbursement for the Surabaya trip",
      "Dewi Ratna Puspita S",
      { email: "dewi.ratna@example.com", role: "Finance staff" },
      "Dewi Ratna Puspita S sent the payment slip for the October honorarium",
      "Dewi Ratna Puspita S",
    ],
    [
      "Bayu Setiawan",
      { email: "adi@vorneo.id", role: "Backend engineer", organization: "Vorneo", summary: "Backend engineer at Vorneo" },
      "Bayu Setiawan reviewed the pull request for the Vorneo listing API",
      "Bayu Settiawan",
      { email: "bayu.settiawan@example.com", organization: "Vorneo" },
      "Bayu Settiawan merged the Vorneo listing API pull request",
      "Bayu Setiawan",
    ],
  ]
  it.each(pairs)("folds %s and a later mention under another email into one person", async (first, firstExtra, firstFact, second, secondExtra, secondFact, kept) => {
    const a = await fact(firstFact)
    await linkMentions(db, scope, [{ mention: mention(first, firstExtra), memoryIds: [a.id] }])
    const b = await fact(secondFact)
    await linkMentions(db, scope, [{ mention: mention(second, secondExtra), memoryIds: [b.id] }])
    const { people } = listPeople(db, { workspaceId: W, container: C })
    expect(people.map((p) => [p.name, p.factCount])).toEqual([[kept, 2]])
    expect(people[0]!.emails).toEqual([firstExtra!.email, secondExtra!.email])
  })

  it("keeps different people with a shared first name apart, even when the context agrees", async () => {
    expect(namesLookAlike("Rizky Kurn", "Rizky Kurniawan")).toBe(true)
    expect(namesLookAlike("Ryan Boyd", "Ryan Castriotta")).toBe(false)
    expect(namesLookAlike("Muhammad Rizki", "Muhammad Fauzan")).toBe(false)
    const same = [
      ["Clara Simmons", "Clara Wong"],
      ["Ryan Boyd", "Ryan Castriotta"],
      ["Muhammad Rizki", "Muhammad Fauzan"],
    ]
    for (const [x, y] of same) {
      const a = await fact(`${x} sent the Vorneo deployment checklist`)
      await linkMentions(db, scope, [{ mention: mention(x!, { organization: "Vorneo" }), memoryIds: [a.id] }])
      const b = await fact(`${y} sent the Vorneo deployment checklist`)
      await linkMentions(db, scope, [{ mention: mention(y!, { organization: "Vorneo" }), memoryIds: [b.id] }])
    }
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(6)
  })

  it("never makes a person of a team mailbox, and never matches people by one", async () => {
    for (const e of ["sekretwargalapor@example.com", "info@vorneo.id", "noreply@github.com", "support+billing@acme.com", "hr@kebunsawit.id", "admin.vorneo@example.com"])
      expect(isSharedMailbox(e), e).toBe(true)
    for (const e of ["clara@vorneo.id", "csaba@example.com", "helena@example.com", "information.zaki@example.com", "hrafn@example.com", "tim@vorneo.id"]) expect(isSharedMailbox(e), e).toBe(false)
    expect(looksLikeOrganization(mention("Sekret WargaLapor", { email: "sekretwargalapor@example.com" }))).toBe(true)
    expect(looksLikeOrganization(mention("PT Vorneo Digital"))).toBe(true)
    expect(looksLikeOrganization(mention("Vorneo Support Team"))).toBe(true)
    expect(looksLikeOrganization(mention("Yayasan Kebun Sawit"))).toBe(true)
    for (const name of ["Clara Simmons", "Contractor 1", "Tim Cook", "Ana Sales", "Mickey Kantor"]) expect(looksLikeOrganization(mention(name)), name).toBe(false)
    expect(looksLikeOrganization(mention("Sekar Ayu Lestari", { email: "sekretwargalapor@example.com" }))).toBe(false)

    // Sekar and Rizky both write from the team's shared mailbox.
    const shared = "sekretwargalapor@example.com"
    const [a, b, c] = await Promise.all([
      fact("Sekret WargaLapor sent the weekly volunteer newsletter"),
      fact("Sekar Ayu Lestari asked for the March report on vaccine complaints"),
      fact("Rizky Kurniawan sent the weekly COVID-19 case recap"),
    ])
    const stats = await linkMentions(db, scope, [
      { mention: mention("Sekret WargaLapor", { email: shared }), memoryIds: [a.id] },
      { mention: mention("Sekar Ayu Lestari", { email: shared, role: "Coordinator" }), memoryIds: [b.id] },
      { mention: mention("Rizky Kurniawan", { email: shared, role: "Data analyst" }), memoryIds: [c.id] },
    ])
    expect(stats).toMatchObject({ created: 2, skipped: 1 })
    const { people } = listPeople(db, { workspaceId: W, container: C, sort: "name" })
    expect(people.map((p) => [p.name, p.emails, p.aliases])).toEqual([
      ["Rizky Kurniawan", [], []],
      ["Sekar Ayu Lestari", [], []],
    ])
    // A person saved before this rule may still carry the mailbox: it no longer pulls others in.
    updatePerson(db, W, people[0]!.id, { emails: [shared] })
    const d = await fact("Clara Simmons replied from the LaporCovid-19 inbox")
    await linkMentions(db, scope, [{ mention: mention("Clara Simmons", { email: shared }), memoryIds: [d.id] }])
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(3)
  })

  it("never creates a person for the container's own subject", async () => {
    expect(isContainerOwner("zaki", mention("Zaki"))).toBe(true)
    expect(isContainerOwner("zaki", mention("Zaki Munziri"))).toBe(true)
    expect(isContainerOwner("zaki-work", mention("zaki"))).toBe(true)
    expect(isContainerOwner("zaki", mention("Muhammad", { aliases: ["Zaki"] }))).toBe(true)
    expect(isContainerOwner("zaki", mention("Someone", { email: "zaki@example.com" }))).toBe(true)
    expect(isContainerOwner("zaki", mention("the user"))).toBe(true)
    expect(isContainerOwner("zaki", mention("Clara Simmons"))).toBe(false)
    expect(isContainerOwner("vorneo", mention("Zaki"))).toBe(false)

    const m = await fact("Zaki lives in Banjarbaru")
    const stats = await linkMentions(db, scope, [
      { mention: mention("Zaki"), memoryIds: [m.id] },
      { mention: mention("me"), memoryIds: [m.id] },
    ])
    expect(stats).toMatchObject({ created: 0, linked: 0, skipped: 2 })
    // In someone else's container, Zaki is a contact like anyone else.
    const other = await fact("Zaki reviews the Vorneo roadmap", W, "vorneo")
    await linkMentions(db, { workspaceId: W, container: "vorneo" }, [{ mention: mention("Zaki"), memoryIds: [other.id] }])
    expect(listPeople(db, { workspaceId: W, container: "vorneo" }).people.map((p) => p.name)).toEqual(["Zaki"])
  })

  it("finds people by search ignoring case and accents", async () => {
    const m = await fact("Élodie Durand translates the French site")
    await linkMentions(db, scope, [{ mention: mention("Élodie Durand", { organization: "Société Générale" }), memoryIds: [m.id] }])
    for (const q of ["elodie", "ÉLODIE", "societe", "durand"]) expect(listPeople(db, { workspaceId: W, container: C, q }).total).toBe(1)
    expect(listPeople(db, { workspaceId: W, container: C, q: "nobody" }).total).toBe(0)
  })

  it("in the default container, treats the workspace owner as the subject", async () => {
    const out: string[] = []
    const io: CliIo = { db: () => db, out: (l) => out.push(l), err: () => {}, readPassword: async () => "a long enough password" }
    await runCli(["create-user", "zaki"], io)
    const ws = (db.$client.prepare("SELECT id FROM organization WHERE slug = 'zaki'").get() as { id: string }).id
    db.$client.prepare("UPDATE user SET name = 'Zaki Munziri' WHERE username = 'zaki'").run()
    const m = await addMemoryDirect(db, { workspaceId: ws, container: "default", fact: "Zaki Munziri met Clara", keywords: "", kind: "dynamic" })
    const stats = await linkMentions(db, { workspaceId: ws, container: "default" }, [
      { mention: mention("Zaki Munziri"), memoryIds: [m.id] },
      { mention: mention("Clara"), memoryIds: [m.id] },
    ])
    expect(stats).toMatchObject({ created: 1, skipped: 1 })
    expect(listPeople(db, { workspaceId: ws, container: "default" }).people.map((p) => p.name)).toEqual(["Clara"])
  })

  it("merges two people and keeps every link, alias and contact", async () => {
    const [a, b, c] = await Promise.all([fact("Ms Simmons sent the mockups"), fact("Clara Simmons leads the redesign"), fact("Ms Simmons and Clara Simmons met")])
    // Names that don't look alike stay two people until someone merges them.
    await linkMentions(db, scope, [{ mention: mention("Ms Simmons", { email: "clara@gmail.com" }), memoryIds: [a.id, c.id] }])
    await linkMentions(db, scope, [{ mention: mention("Clara Simmons", { role: "Designer", organization: "Vorneo" }), memoryIds: [b.id, c.id] }])
    const rows = listPeople(db, { workspaceId: W, container: C, sort: "name" }).people
    expect(rows.map((p) => p.name)).toEqual(["Clara Simmons", "Ms Simmons"])
    const [full, short] = rows as [(typeof rows)[0], (typeof rows)[0]]

    const kept = mergePeople(db, W, full.id, short.id)
    expect(kept).toMatchObject({ name: "Clara Simmons", role: "Designer", emails: ["clara@gmail.com"], factCount: 3 })
    expect(kept.aliases).toContain("Ms Simmons")
    expect(getPerson(db, W, short.id)).toBeNull()
    // The old name now resolves to the kept person.
    const d = await fact("Ms Simmons booked a room")
    await linkMentions(db, scope, [{ mention: mention("ms simmons"), memoryIds: [d.id] }])
    expect(listPeople(db, { workspaceId: W, container: C }).people.map((p) => [p.name, p.factCount])).toEqual([["Clara Simmons", 4]])
    expect(() => mergePeople(db, W, full.id, full.id)).toThrow(/two different/)
    expect(() => mergePeople(db, "ws_other", full.id, short.id)).toThrow(/No such person/)
  })

  it("keeps hand edits when extraction mentions the person again", async () => {
    const a = await fact("Budi handles payments")
    await linkMentions(db, scope, [{ mention: mention("Budi", { role: "Accountant" }), memoryIds: [a.id] }])
    const budi = listPeople(db, { workspaceId: W, container: C }).people[0]!
    updatePerson(db, W, budi.id, { name: "Budi Santoso", role: "Finance lead", aliases: ["Budi"] })
    const b = await fact("Budi approved the budget")
    await linkMentions(db, scope, [{ mention: mention("Budi", { role: "Clerk", email: "budi@ks.id" }), memoryIds: [b.id] }])
    const after = getPerson(db, W, budi.id)!.person
    expect(after).toMatchObject({ name: "Budi Santoso", role: "Finance lead", emails: ["budi@ks.id"], manual: true, factCount: 2 })
  })

  it("carries links to the new version when a fact is edited", async () => {
    const a = await fact("Clara leads the redesign")
    await linkMentions(db, scope, [{ mention: mention("Clara"), memoryIds: [a.id] }])
    const next = await editMemory(db, W, a.id, { fact: "Clara leads the redesign and the app" })
    expect(peopleForMemories(db, W, [next!.id])[next!.id]!.map((p) => p.name)).toEqual(["Clara"])
  })
})

// Writes a person row as the old resolver left it, bypassing matching.
async function seedPerson(name: string, extra: Partial<{ emails: string[]; handles: string[]; aliases: string[]; role: string; organization: string; summary: string }>, facts: string[], container = C) {
  const id = `per_${name.replace(/\W+/g, "_")}_${container}`
  db.insert(people).values({ id, workspaceId: W, container, name, ...extra }).run()
  for (const f of facts) db.insert(personMemories).values({ personId: id, memoryId: (await fact(f, W, container)).id, workspaceId: W }).run()
  return id
}

describe("merge suggestions", () => {
  it("pairs people who share a contact or a look-alike name the embedding agrees on, and merges nothing", async () => {
    const ferd = await seedPerson("Rizky Kurn", { emails: ["rizky@wargalapor.example", "rizky.kurniawan@example.com"] }, ["Rizky Kurn shared the volunteer schedule"])
    const rizky = await seedPerson("Rizky Kurniawan", { emails: ["rizky.kurniawan@example.com", "rizky@wargalapor.example"], role: "Data analyst" }, [
      "Rizky Kurniawan sent the weekly COVID-19 case recap",
      "Rizky Kurniawan fixed the Jakarta numbers",
    ])
    const maya = await seedPerson("Maya Lee Hartono", { emails: ["maya@foundation.example"], role: "Program officer", summary: "Program officer handling the grant" }, [
      "Maya Lee Hartono sent the grant agreement for signature",
    ])
    const hartono = await seedPerson("Maya Hartono", { emails: ["maya.hartono@example.com"], role: "Program officer" }, ["Maya Hartono scheduled a call to review the quarterly grant report"])
    await seedPerson("Slack Ryan", { handles: ["@ryan"] }, ["Slack Ryan posted the deploy log"])
    await seedPerson("Ryan Boyd", { handles: ["ryan"] }, ["Ryan Boyd posted the deploy log", "Ryan Boyd rolled back the release"])
    // Never suggested: different people, a first name with another email, or a team row.
    await seedPerson("Clara Simmons", { emails: ["clara@vorneo.id"], organization: "Vorneo" }, ["Clara Simmons sent the Vorneo deployment checklist"])
    await seedPerson("Clara Wong", { emails: ["clara.wong@acme.com"], organization: "Vorneo" }, ["Clara Wong sent the Vorneo deployment checklist"])
    await seedPerson("Clara", { emails: ["clara@gmail.com"] }, ["Clara sent the Vorneo deployment checklist"])
    await seedPerson("Muhammad Rizki", { aliases: ["Muhammad"] }, ["Muhammad Rizki joined the standup"])
    await seedPerson("Muhammad Fauzan", { aliases: ["Muhammad"] }, ["Muhammad Fauzan joined the standup"])
    await seedPerson("Sekret WargaLapor", { aliases: ["Sekar Ayu Lestari"], emails: ["sekretwargalapor@example.com"] }, ["Sekret WargaLapor sent the newsletter"])
    await seedPerson("Sekar Ayu Lestari", { emails: ["sekretwargalapor@example.com"] }, ["Sekar Ayu Lestari asked for the March report"])
    // Another container's twin is out of scope.
    await seedPerson("Rizky Kurn", { emails: ["rizky@wargalapor.example"] }, ["Rizky Kurn joined"], "vorneo")

    const before = listPeople(db, { workspaceId: W, container: C }).total
    const found = await suggestPeopleMerges(db, scope)
    expect(found.map((s) => [s.keep.name, s.drop.name, s.reasons])).toEqual([
      ["Rizky Kurniawan", "Rizky Kurn", ["same email rizky@wargalapor.example", "same email rizky.kurniawan@example.com"]],
      ["Ryan Boyd", "Slack Ryan", ["same handle @ryan"]],
      ["Maya Lee Hartono", "Maya Hartono", ["names look alike"]],
    ])
    expect(found.map((s) => [s.keep.id, s.drop.id])).toEqual([
      [rizky, ferd],
      expect.any(Array),
      [maya, hartono],
    ])
    expect(found[0]!.similarity).toBeNull()
    expect(found[2]!.similarity).toBeGreaterThanOrEqual(0.855)
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(before)
  })
})

describe("tenant isolation", () => {
  const A = "ws_alpha"
  const B = "ws_beta"

  it("never shows, links or merges a person across workspaces", async () => {
    setDb(db)
    const a = await fact("Clara leads the redesign", A, "shared")
    const b = await fact("Clara runs payroll", B, "shared")
    await linkMentions(db, { workspaceId: A, container: "shared" }, [{ mention: mention("Clara"), memoryIds: [a.id] }])
    // A memory id from workspace A, offered while linking in B, is ignored.
    const stats = await linkMentions(db, { workspaceId: B, container: "shared" }, [{ mention: mention("Clara"), memoryIds: [b.id, a.id] }])
    expect(stats.linked).toBe(1)
    const ca = listPeople(db, { workspaceId: A, container: "shared" }).people[0]!
    const cb = listPeople(db, { workspaceId: B, container: "shared" }).people[0]!
    expect(ca.id).not.toBe(cb.id)
    expect(getPerson(db, A, ca.id)!.facts.map((f) => f.id)).toEqual([a.id])
    expect(getPerson(db, B, ca.id)).toBeNull()
    expect(() => personApi(db, B, ca.id)).toThrow(/no such person/)
    expect(listPeopleApi(db, B, { container: "shared" }).people.map((p) => p.id)).toEqual([cb.id])
    expect(peopleForMemories(db, B, [a.id])).toEqual({})
    expect(() => mergePeople(db, B, cb.id, ca.id)).toThrow(/No such person/)
    expect(deletePerson(db, B, ca.id)).toBe(false)
    expect(updatePerson(db, B, ca.id, { name: "Hijacked" })).toBeNull()

    // Search results only carry the searching workspace's people.
    const res = await search(db, B, { container: "shared", q: "Clara", limit: 5 })
    const hit = res.results.find((r) => r.type === "memory" && r.id === b.id)
    expect(hit && "people" in hit ? hit.people.map((p) => p.id) : []).toEqual([cb.id])

    // REST: a key from B gets 404 for A's person and only B's list.
    const { key } = createApiKey(db, { workspaceId: B, name: "test" })
    type Handler = (ctx: { request: Request; params: Record<string, string> }) => Promise<Response>
    const handler = (route: unknown) => (route as { options: { server: { handlers: Record<string, Handler> } } }).options.server.handlers.GET!
    const req = (path: string) => new Request(`http://localhost${path}`, { headers: { authorization: `Bearer ${key}` } })
    const one = await handler(PersonRoute)({ request: req(`/v1/people/${ca.id}`), params: { id: ca.id } })
    expect(one.status).toBe(404)
    const mine = await handler(PersonRoute)({ request: req(`/v1/people/${cb.id}`), params: { id: cb.id } })
    expect((await mine.json()).facts.map((f: MemoryView) => f.id)).toEqual([b.id])
    const all = await handler(PeopleRoute)({ request: req("/v1/people?container=shared"), params: {} })
    expect((await all.json()).people.map((p: { id: string }) => p.id)).toEqual([cb.id])
    setDb(undefined)
  })
})

describe("backfill", () => {
  it("links existing facts once, and a rerun sends nothing to the model", async () => {
    const facts = await Promise.all([
      fact("Works with Clara Simmons at Vorneo"),
      fact("Budi Santoso handles payments for Kebun Sawit"),
      fact("Prefers Puma running shoes"),
      fact("Zaki lives in Banjarbaru"),
    ])
    const { chat, calls, systems } = scripted([
      {
        people: [
          { name: "Clara Simmons", organization: "Vorneo", facts: [1] },
          { name: "Budi Santoso", facts: ["2"] },
          { name: "Zaki", facts: [4] },
          { name: "Nobody", facts: [99] },
        ],
      },
    ])
    const first = await backfillPeople(db, scope, { chat })
    expect(first).toEqual({ scanned: 4, linked: 2, created: 2, calls: 1 })
    expect(systems[0]).toContain('memory named "zaki"')
    expect(peopleForMemories(db, W, [facts[0]!.id])[facts[0]!.id]!.map((p) => p.name)).toEqual(["Clara Simmons"])

    const again = await backfillPeople(db, scope, { chat })
    expect(again).toEqual({ scanned: 0, linked: 0, created: 0, calls: 0 })
    expect(calls).toHaveBeenCalledTimes(1)
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(2)

    // A new fact is the only one the next run sends.
    await fact("Clara Simmons booked the design review")
    const { chat: chat2, calls: calls2 } = scripted([{ people: [{ name: "Clara Simmons", facts: [1] }] }])
    expect(await backfillPeople(db, scope, { chat: chat2 })).toEqual({ scanned: 1, linked: 1, created: 0, calls: 1 })
    expect(String((calls2.mock.calls[0]![0] as { prompt: string }).prompt)).toContain("booked the design review")
  })

  it("runs one batch per job and queues the next until every fact is checked", async () => {
    for (let i = 0; i < 41; i++) await fact(`Note ${i} about the roadmap`)
    const { chat, calls } = scripted([{ people: [] }, { people: [] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    const log = vi.spyOn(console, "log").mockImplementation(() => {})
    enqueue(db, W, "people_backfill", { container: C })
    await drain(runner)
    log.mockRestore()
    const backfills = db.select().from(jobs).all().filter((j) => j.type === "people_backfill")
    expect(backfills.map((j) => j.status)).toEqual(["done", "done"])
    expect(calls.mock.calls.filter((c) => !(c[0] as { system: string }).system.includes("short profile"))).toHaveLength(2)
  })

  it("runs as a job, one queued per container", async () => {
    await fact("Works with Clara Simmons at Vorneo")
    expect(enqueue(db, W, "people_backfill", { container: C })).not.toBeNull()
    expect(enqueue(db, W, "people_backfill", { container: C })).toBeNull()
    const { chat } = scripted([{ people: [{ name: "Clara Simmons", facts: [1] }] }])
    const runner = new JobRunner(db, createHandlers(db, { chat, now: () => new Date() }), () => {})
    const log = vi.spyOn(console, "log").mockImplementation(() => {})
    await drain(runner)
    log.mockRestore()
    expect(db.select().from(jobs).all().map((j) => [j.type, j.status])).toContainEqual(["people_backfill", "done"])
    expect(listPeople(db, { workspaceId: W, container: C }).total).toBe(1)
  })

  it("backfill-people queues jobs, or runs them with --now", async () => {
    const out: string[] = []
    const err: string[] = []
    const backfill = vi.fn(async () => ({ scanned: 1, linked: 1, created: 1, calls: 1 }))
    const io: CliIo = { db: () => db, out: (l) => out.push(l), err: (l) => err.push(l), readPassword: async () => "a long enough password", backfill }
    await runCli(["create-user", "zaki"], io)
    const ws = (db.$client.prepare("SELECT id FROM organization WHERE slug = 'zaki'").get() as { id: string }).id
    await addMemoryDirect(db, { workspaceId: ws, container: "zaki", fact: "Works with Clara", keywords: "", kind: "dynamic" })
    await addMemoryDirect(db, { workspaceId: ws, container: "vorneo", fact: "Budi owns the brief", keywords: "", kind: "dynamic" })
    out.length = 0

    expect(await runCli(["backfill-people", "--container", "zaki"], io)).toBe(0)
    expect(out).toEqual([expect.stringMatching(/^zaki\/zaki {2}1 fact\(s\) to check {2}queued job \d+$/)])
    expect(await runCli(["backfill-people", "--workspace", "zaki", "--now", "--json"], io)).toBe(0)
    expect(JSON.parse(out.at(-1)!)).toEqual([
      expect.objectContaining({ container: "vorneo", stats: expect.objectContaining({ linked: 1 }) }),
      expect.objectContaining({ container: "zaki", stats: expect.objectContaining({ linked: 1 }) }),
    ])
    expect(backfill).toHaveBeenCalledTimes(2)
    expect(await runCli(["backfill-people", "--workspace", "nope"], io)).toBe(1)
  })
})

describe("ask", () => {
  it("answers questions about a person from their page", async () => {
    const a = await fact("Works with Clara Simmons, a product designer at Vorneo, on the listing redesign")
    const b = await fact("Meets Clara every Tuesday to review Figma mockups")
    await fact("Prefers Puma running shoes")
    await linkMentions(db, scope, [
      { mention: mention("Clara Simmons", { aliases: ["Clara"], role: "Product designer", organization: "Vorneo", email: "clara@vorneo.id" }), memoryIds: [a.id, b.id] },
    ])
    expect(peopleNamedIn(db, scope, "what do I know about clara?").map((p) => p.name)).toEqual(["Clara Simmons"])
    expect(peopleNamedIn(db, scope, "what shoes do I wear?")).toEqual([])
    // A one-word name that is also a word needs a capital letter, unless the question is all lowercase.
    const will = await fact("Will fixes the office printer")
    await linkMentions(db, scope, [{ mention: mention("Will"), memoryIds: [will.id] }])
    expect(peopleNamedIn(db, scope, "What will I do next week?")).toEqual([])
    expect(peopleNamedIn(db, scope, "What does Will do?").map((p) => p.name)).toEqual(["Will"])
    expect(peopleNamedIn(db, scope, "what does will do?").map((p) => p.name)).toEqual(["Will"])

    const prompts: string[] = []
    const chat = vi.fn(async (req: { prompt: string; schema: { parse: (x: unknown) => unknown } }) => {
      prompts.push(req.prompt)
      return { data: req.schema.parse({ answer: "Clara Simmons is a product designer at Vorneo [m1]. You meet her on Tuesdays [m2].", covered: true }), model: "m" }
    })
    const out = await answerQuestion(db, W, { container: C, question: "What do I know about Clara?", includeChunks: false }, {
      chat: chat as unknown as AskDeps["chat"],
      now: () => new Date("2026-11-10"),
    })
    expect(prompts[0]).toMatch(/PEOPLE named in the question/)
    expect(prompts[0]).toMatch(/Clara Simmons; also called Clara; Product designer at Vorneo; contact clara@vorneo\.id/)
    // Her facts come first, ahead of the search hits.
    expect(prompts[0]).toMatch(/\[m1\][^\n]*(Clara|Figma)/)
    expect(out.people.map((p) => p.name)).toEqual(["Clara Simmons"])
    expect(out.citations.map((c) => c.id).sort()).toEqual([a.id, b.id].sort())
  })
})
