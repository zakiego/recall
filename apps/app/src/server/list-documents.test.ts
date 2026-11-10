// listDocuments pages newest first with a (createdAt, id) keyset cursor.
import { beforeEach, describe, expect, it } from "vitest"
import { openDb, type DB } from "./db"
import { countDocuments, listDocuments } from "./memories"

const W = "ws_docs"
const C = "zaki"
const T0 = Date.UTC(2026, 0, 1)
let db: DB
beforeEach(() => {
  db = openDb(":memory:")
})

function doc(id: string, createdAt: number, container = C) {
  db.$client
    .prepare("INSERT INTO documents (id, workspace_id, container, source_type, title, status, created_at) VALUES (?, ?, ?, 'email', ?, 'done', ?)")
    .run(id, W, container, `Title ${id}`, createdAt)
}

function pad(n: number) {
  return String(n).padStart(4, "0")
}

// Every page from the first to the last, following nextCursor.
function allPages(limit: number) {
  const ids: string[][] = []
  let cursor: string | null = null
  do {
    const page = listDocuments(db, W, C, { cursor, limit })
    ids.push(page.documents.map((d) => d.id))
    cursor = page.nextCursor
  } while (cursor && ids.length < 100)
  return ids
}

describe("listDocuments", () => {
  it("defaults to the newest 50 and hands back a cursor for the rest", () => {
    for (let i = 0; i < 120; i++) doc(`doc_${pad(i)}`, T0 - i * 1000)
    const page = listDocuments(db, W, C)
    expect(page.documents).toHaveLength(50)
    expect(page.documents[0].id).toBe("doc_0000")
    expect(page.nextCursor).not.toBeNull()
    expect(countDocuments(db, W, C)).toBe(120)
  })

  it("reaches every document exactly once, oldest last", () => {
    for (let i = 0; i < 120; i++) doc(`doc_${pad(i)}`, T0 - i * 1000)
    const pages = allPages(50)
    expect(pages.map((p) => p.length)).toEqual([50, 50, 20])
    expect(pages.flat()).toEqual(Array.from({ length: 120 }, (_, i) => `doc_${pad(i)}`))
  })

  it("splits documents created in the same millisecond by id, without repeats or gaps", () => {
    for (let i = 0; i < 7; i++) doc(`doc_same_${i}`, T0)
    doc("doc_older", T0 - 1)
    const pages = allPages(3)
    expect(pages.map((p) => p.length)).toEqual([3, 3, 2])
    expect(pages.flat()).toEqual([...Array.from({ length: 7 }, (_, i) => `doc_same_${6 - i}`), "doc_older"])
  })

  it("keeps the next page stable when a newer document arrives in between", () => {
    for (let i = 0; i < 4; i++) doc(`doc_${i}`, T0 - i * 1000)
    const first = listDocuments(db, W, C, { limit: 2 })
    doc("doc_new", T0 + 1000)
    const second = listDocuments(db, W, C, { cursor: first.nextCursor, limit: 2 })
    expect(second.documents.map((d) => d.id)).toEqual(["doc_2", "doc_3"])
    expect(second.nextCursor).toBeNull()
  })

  it("returns no cursor on the last page, ignores a bad cursor, and caps the limit", () => {
    for (let i = 0; i < 250; i++) doc(`doc_${pad(i)}`, T0 - i * 1000)
    expect(listDocuments(db, W, C, { limit: 1000 }).documents).toHaveLength(200)
    expect(listDocuments(db, W, C, { cursor: "not-a-cursor", limit: 2 }).documents.map((d) => d.id)).toEqual(["doc_0000", "doc_0001"])
    expect(listDocuments(db, W, C, { limit: 250 }).nextCursor).not.toBeNull()
    const tail = listDocuments(db, W, C, { cursor: listDocuments(db, W, C, { limit: 200 }).nextCursor, limit: 200 })
    expect(tail.documents).toHaveLength(50)
    expect(tail.nextCursor).toBeNull()
  })

  it("stays inside its container", () => {
    doc("doc_mine", T0)
    doc("doc_other", T0, "other")
    expect(listDocuments(db, W, C).documents.map((d) => d.id)).toEqual(["doc_mine"])
    expect(countDocuments(db, W, C)).toBe(1)
  })
})
