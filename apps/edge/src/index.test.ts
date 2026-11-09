import { afterEach, describe, expect, it, vi } from "vitest"
import worker from "./index.ts"

const SITE = "https://recall-site-emu.pages.dev"
const bindings = { SITE_ORIGIN: SITE }

afterEach(() => vi.unstubAllGlobals())

function stubFetch(res: () => Response) {
  const calls: { input: Request; init?: RequestInit }[] = []
  vi.stubGlobal("fetch", async (input: Request, init?: RequestInit) => {
    calls.push({ input, init })
    return res()
  })
  return calls
}

describe("edge worker", () => {
  it("hands app requests to the origin as the very same Request", async () => {
    const upstream = new Response("data: x\n\n", {
      headers: { "set-cookie": "recall.session_token=a; Path=/; HttpOnly", "content-type": "text/event-stream" },
    })
    const calls = stubFetch(() => upstream)
    const req = new Request("https://recall.zakiego.com/v1/ask?x=1", { method: "POST", body: "{}", headers: { authorization: "Bearer rcl_x" } })
    const res = await worker.fetch(req, bindings)
    expect(calls).toHaveLength(1)
    expect(calls[0].input).toBe(req)
    expect(res).toBe(upstream)
    expect(res.headers.get("set-cookie")).toContain("recall.session_token")
  })

  it("passes origin redirects through untouched", async () => {
    stubFetch(() => new Response(null, { status: 302, headers: { location: "/app/login" } }))
    const res = await worker.fetch(new Request("https://recall.zakiego.com/login"), bindings)
    expect(res.status).toBe(302)
    expect(res.headers.get("location")).toBe("/app/login")
  })

  it("serves the rest from Pages, keeping path, query and headers", async () => {
    const calls = stubFetch(() => new Response("<html>"))
    await worker.fetch(new Request("https://recall.zakiego.com/docs/api?q=1", { headers: { accept: "text/html" } }), bindings)
    expect(calls[0].input.url).toBe(`${SITE}/docs/api?q=1`)
    expect(calls[0].input.headers.get("accept")).toBe("text/html")
    expect(calls[0].init?.redirect).toBe("manual")
  })

  it("keeps Pages redirects on the public hostname", async () => {
    stubFetch(() => new Response(null, { status: 308, headers: { location: `${SITE}/docs` } }))
    const res = await worker.fetch(new Request("https://recall.zakiego.com/docs/"), bindings)
    expect(res.status).toBe(308)
    expect(res.headers.get("location")).toBe("https://recall.zakiego.com/docs")
  })

  it("on the preview worker, forwards app paths to APP_ORIGIN", async () => {
    const calls = stubFetch(() => new Response("ok"))
    await worker.fetch(new Request("https://recall-edge-preview.example.workers.dev/health"), {
      SITE_ORIGIN: SITE,
      APP_ORIGIN: "https://recall.zakiego.com",
    })
    expect(calls[0].input.url).toBe("https://recall.zakiego.com/health")
  })
})
