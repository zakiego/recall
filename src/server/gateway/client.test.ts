import fs from "node:fs"
import path from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { chatJson, extractJson, GatewayError, InvalidJsonError, readStreamText } from "./client"
import { extractResult } from "./schemas"

const fixture = (name: string) => fs.readFileSync(path.join(import.meta.dirname, "fixtures", name), "utf8")

function sseBody(text: string) {
  return new Response(text).body!
}

function sseResponse(text: string) {
  return new Response(text, { headers: { "content-type": "text/event-stream" } })
}

describe("readStreamText", () => {
  it("joins the text parts of a recorded gateway stream", async () => {
    const text = await readStreamText(sseBody(fixture("extract-ok.sse")))
    expect(JSON.parse(text)).toEqual({
      add: [{ fact: "Loves Adidas shoes", keywords: "sepatu sneakers", kind: "dynamic" }],
      update: [],
      invalidate: [],
    })
  })

  it("throws on an in-stream error chunk", async () => {
    await expect(readStreamText(sseBody(fixture("error.sse")))).rejects.toThrow(GatewayError)
  })
})

describe("extractJson", () => {
  it("unwraps fences and prose", () => {
    expect(extractJson('Sure!\n```json\n{"a":1}\n```')).toEqual({ a: 1 })
    expect(extractJson('Result: {"a":{"b":2}} done')).toEqual({ a: { b: 2 } })
  })
  it("throws InvalidJsonError", () => {
    expect(() => extractJson("no json here")).toThrow(InvalidJsonError)
  })
})

describe("chatJson", () => {
  const env = { ...process.env }
  beforeEach(() => {
    process.env.GATEWAY_KEY = "zkg_test"
    process.env.GATEWAY_URL = "http://gateway.test"
    process.env.EXTRACT_MODEL = "claude-haiku-5-5"
    process.env.FALLBACK_MODEL = "claude-haiku-4-5"
  })
  afterEach(() => {
    process.env = { ...env }
  })

  it("parses and normalizes a fenced reply", async () => {
    const fetchMock = vi.fn(async () => sseResponse(fixture("extract-fenced.sse")))
    const out = await chatJson({ system: "s", prompt: "p", schema: extractResult }, fetchMock as typeof fetch)
    expect(out).toEqual({
      add: [],
      update: [{ ref: 2, fact: "Switched to Puma running shoes", keywords: "puma sepatu" }],
      invalidate: [3],
    })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe("http://gateway.test/v1/chat")
    const body = JSON.parse(init.body as string)
    expect(body).toMatchObject({ model: "claude-haiku-5-5", system: "s" })
    expect(body.id).toBeUndefined()
    expect((init.headers as Record<string, string>).authorization).toBe("Bearer zkg_test")
  })

  it("retries once on invalid JSON", async () => {
    const bad = [
      { type: "start" },
      { type: "text-start", id: "t0" },
      { type: "text-delta", id: "t0", delta: "I could not find anything." },
      { type: "text-end", id: "t0" },
      { type: "finish", finishReason: "stop" },
    ]
      .map((c) => `data: ${JSON.stringify(c)}\n\n`)
      .join("")
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(sseResponse(bad))
      .mockResolvedValueOnce(sseResponse(fixture("extract-ok.sse")))
    const out = await chatJson({ system: "s", prompt: "p", schema: extractResult }, fetchMock as typeof fetch)
    expect(out.add[0]?.fact).toBe("Loves Adidas shoes")
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const retryBody = JSON.parse((fetchMock.mock.calls[1]![1] as RequestInit).body as string)
    expect(retryBody.messages[0].parts[0].text).toMatch(/not valid JSON/)
  })

  it("falls back when the model is rejected", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ error: { type: "model_not_allowed", message: "no" } }, { status: 403 }))
      .mockResolvedValueOnce(sseResponse(fixture("extract-ok.sse")))
    await chatJson({ system: "s", prompt: "p", schema: extractResult }, fetchMock as typeof fetch)
    const second = JSON.parse((fetchMock.mock.calls[1]![1] as RequestInit).body as string)
    expect(second.model).toBe("claude-haiku-4-5")
  })

  it("surfaces 429 with retry-after", async () => {
    const fetchMock = vi.fn(async () =>
      Response.json({ error: { type: "overloaded", message: "busy" } }, { status: 429, headers: { "retry-after": "10" } }),
    )
    const err = await chatJson({ system: "s", prompt: "p", schema: extractResult }, fetchMock as typeof fetch).catch((e) => e)
    expect(err).toBeInstanceOf(GatewayError)
    expect(err).toMatchObject({ status: 429, type: "overloaded", retryAfterMs: 10_000 })
  })
})
