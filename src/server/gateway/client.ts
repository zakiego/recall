import { parseJsonEventStream, readUIMessageStream, uiMessageChunkSchema, type UIMessage, type UIMessageChunk } from "ai"
import type { z } from "zod"
import { config } from "../config"

export class GatewayError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly type: string,
    readonly retryAfterMs?: number,
  ) {
    super(message)
    this.name = "GatewayError"
  }
}

export class InvalidJsonError extends Error {
  constructor(
    message: string,
    readonly text: string,
  ) {
    super(message)
    this.name = "InvalidJsonError"
  }
}

// Turns an SSE body from /v1/chat into the final assistant text. Uses the AI
// SDK's own reader so it assembles parts the same way useChat would.
export async function readStreamText(body: ReadableStream<Uint8Array>): Promise<string> {
  let streamError: string | undefined
  const chunks = parseJsonEventStream({ stream: body, schema: uiMessageChunkSchema }).pipeThrough(
    new TransformStream<{ success: true; value: UIMessageChunk } | { success: false; error: unknown }, UIMessageChunk>({
      transform(result, controller) {
        if (!result.success) {
          controller.error(new GatewayError(`bad stream chunk: ${String(result.error)}`, 502, "bad_stream"))
          return
        }
        if (result.value.type === "error") streamError = result.value.errorText
        controller.enqueue(result.value)
      },
    }),
  )
  let last: UIMessage | undefined
  try {
    for await (const message of readUIMessageStream({ stream: chunks, terminateOnError: true })) {
      last = message
    }
  } catch (err) {
    if (err instanceof GatewayError) throw err
    throw new GatewayError(streamError ?? (err instanceof Error ? err.message : String(err)), 502, "stream_error")
  }
  if (streamError) throw new GatewayError(streamError, 502, "stream_error")
  const text = (last?.parts ?? [])
    .filter((p): p is { type: "text"; text: string } => p.type === "text")
    .map((p) => p.text)
    .join("")
  return text
}

// Models wrap JSON in fences or add a sentence despite the prompt. Take the
// outermost object.
export function extractJson(text: string): unknown {
  const fenced = text.match(/```(?:json)?\s*([\s\S]*?)```/)
  const body = (fenced?.[1] ?? text).trim()
  try {
    return JSON.parse(body)
  } catch {
    const start = body.indexOf("{")
    const end = body.lastIndexOf("}")
    if (start >= 0 && end > start) {
      try {
        return JSON.parse(body.slice(start, end + 1))
      } catch {
        /* fall through */
      }
    }
    throw new InvalidJsonError("model did not return JSON", text)
  }
}

export type ChatRequest = { system: string; prompt: string; model?: string }

export async function chatOnce(req: ChatRequest, fetchImpl: typeof fetch = fetch): Promise<string> {
  const cfg = config()
  if (!cfg.gatewayKey) throw new GatewayError("GATEWAY_KEY is not set", 0, "config")
  const res = await fetchImpl(`${cfg.gatewayUrl}/v1/chat`, {
    method: "POST",
    headers: { authorization: `Bearer ${cfg.gatewayKey}`, "content-type": "application/json" },
    body: JSON.stringify({
      model: req.model ?? cfg.extractModel,
      effort: "low",
      system: req.system,
      messages: [{ id: "u1", role: "user", parts: [{ type: "text", text: req.prompt }] }],
    }),
    signal: AbortSignal.timeout(180_000),
  })
  if (!res.ok || !res.body) {
    let type = "http_error"
    let message = `gateway returned ${res.status}`
    try {
      const j = (await res.json()) as { error?: { type?: string; message?: string } }
      type = j.error?.type ?? type
      message = j.error?.message ?? message
    } catch {
      /* not JSON */
    }
    const ra = Number(res.headers.get("retry-after"))
    throw new GatewayError(message, res.status, type, Number.isFinite(ra) && ra > 0 ? ra * 1000 : undefined)
  }
  return readStreamText(res.body)
}

// One structured call: JSON in the reply, validated by zod. Invalid JSON gets
// one retry with a reminder. A rejected model falls back to FALLBACK_MODEL.
export async function chatJson<T extends z.ZodType>(
  req: ChatRequest & { schema: T },
  fetchImpl: typeof fetch = fetch,
): Promise<z.infer<T>> {
  const cfg = config()
  let model = req.model ?? cfg.extractModel
  let prompt = req.prompt
  for (let attempt = 0; attempt < 2; attempt++) {
    let text: string
    try {
      text = await chatOnce({ system: req.system, prompt, model }, fetchImpl)
    } catch (err) {
      const rejected = err instanceof GatewayError && (err.type === "model_not_allowed" || (err.status === 400 && /model/i.test(err.message)))
      if (rejected && model !== cfg.fallbackModel) {
        console.warn(`[recall] gateway rejected ${model}, falling back to ${cfg.fallbackModel}`)
        model = cfg.fallbackModel
        text = await chatOnce({ system: req.system, prompt, model }, fetchImpl)
      } else throw err
    }
    try {
      return req.schema.parse(extractJson(text))
    } catch (err) {
      if (attempt === 1) throw err instanceof InvalidJsonError ? err : new InvalidJsonError(`invalid shape: ${String(err)}`, text)
      prompt = `${req.prompt}\n\nYour last reply was not valid JSON for the required shape. Reply again with only the JSON object.`
    }
  }
  throw new Error("unreachable")
}
