// Reads a password for the admin CLI. Piped stdin is read whole, so
// `echo pw | cli create-user x` keeps working. On a terminal the prompt goes
// to `output` (stderr in the CLI, so stdout carries only the result) and the
// typed characters are never echoed back.
import { createInterface } from "node:readline/promises"
import { Writable, type Readable } from "node:stream"

type Input = Readable & { isTTY?: boolean }

export async function readPassword(input: Input, output: Writable, prompt = "Password (12+ chars): "): Promise<string> {
  if (!input.isTTY) {
    let data = ""
    for await (const chunk of input) data += chunk
    return data.trim()
  }
  output.write(prompt)
  // readline echoes what it reads to its output; give it one that drops everything.
  const silent = new Writable({ write: (_chunk, _enc, done) => done() })
  const rl = createInterface({ input, output: silent, terminal: true })
  const cancel = new AbortController()
  rl.on("SIGINT", () => cancel.abort())
  try {
    return (await rl.question("", { signal: cancel.signal })).trim()
  } catch (err) {
    if (cancel.signal.aborted) throw new Error("cancelled")
    throw err
  } finally {
    rl.close()
    output.write("\n")
  }
}
