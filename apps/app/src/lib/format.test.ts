import { describe, expect, it } from "vitest"
import { ago, explainError } from "./format"
import { RpcError } from "./rpc-client"

describe("ago", () => {
  const now = Date.UTC(2026, 10, 17, 12, 0)
  it("is relative under 30 days and a plain date after", () => {
    expect(ago(now - 3 * 86_400_000, now)).toBe("3 days ago")
    expect(ago(Date.UTC(2026, 9, 9, 15, 30), now)).toBe("Oct 9, 2026")
  })
})

describe("explainError", () => {
  it("says the model is missing and offers no retry", () => {
    const e = explainError(new RpcError("onnxruntime library not found (set ONNXRUNTIME_LIB): looked in [/x]", { status: 500, code: "ERROR" }), "Search", "Search failed.")
    expect(e).toEqual({
      message: "Search needs the embedding model, which isn't installed on this server.",
      detail: "onnxruntime library not found (set ONNXRUNTIME_LIB): looked in [/x]",
      retry: false,
    })
  })

  it("maps a turned-off job loop", () => {
    const e = explainError(new RpcError("The job loop is off on this server (RECALL_JOBS=off), so nothing can answer.", { status: 503, code: "ERROR" }), "Ask", "x")
    expect(e.message).toBe("Ask is turned off on this server.")
    expect(e.retry).toBe(false)
  })

  it("falls back to the caller's sentence and keeps the raw text", () => {
    expect(explainError(new Error("boom"), "Search", "The search didn't run.")).toEqual({ message: "The search didn't run.", detail: "boom", retry: true })
  })
})
