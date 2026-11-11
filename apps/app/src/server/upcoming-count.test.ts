// countUpcoming counts what listUpcoming would return without its row limit,
// so the Upcoming page's window counts don't stop at 200.
import { beforeEach, describe, expect, it } from "vitest"
import { openDb, type DB } from "./db"
import { countUpcoming, listUpcoming } from "./memories"

const W = "ws_upcoming"
const C = "zaki"
let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

function dueFact(id: string, due: string, opts: { container?: string; validTo?: number } = {}) {
  db.$client
    .prepare(
      "INSERT INTO memories (id, workspace_id, container, fact, due_date, due_precision, valid_to, created_at) VALUES (?, ?, ?, ?, ?, 'day', ?, ?)",
    )
    .run(id, W, opts.container ?? C, `fact ${id}`, due, opts.validTo ?? null, Date.UTC(2026, 0, 1))
}

describe("countUpcoming", () => {
  it("counts past the list's limit", () => {
    for (let i = 0; i < 250; i++) dueFact(`m_${i}`, `2026-12-${String((i % 28) + 1).padStart(2, "0")}`)
    const range = { workspaceId: W, container: C, from: "2026-12-01", to: "2026-12-31" }
    expect(listUpcoming(db, range)).toHaveLength(200)
    expect(countUpcoming(db, range)).toBe(250)
  })

  it("applies the same range, container and current-only filters as the list", () => {
    dueFact("in", "2026-12-10")
    dueFact("edge", "2026-12-31")
    dueFact("later", "2027-01-01")
    dueFact("closed", "2026-12-10", { validTo: Date.UTC(2026, 5, 1) })
    dueFact("other", "2026-12-10", { container: "work" })
    const range = { workspaceId: W, from: "2026-12-01", to: "2026-12-31" }
    expect(countUpcoming(db, { ...range, container: C })).toBe(2)
    expect(listUpcoming(db, { ...range, container: C }).map((m) => m.id).sort()).toEqual(["edge", "in"])
    // No container: every container in the workspace.
    expect(countUpcoming(db, range)).toBe(3)
  })
})
