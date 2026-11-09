import { PassThrough } from "node:stream"
import { describe, expect, it } from "vitest"
import { readPassword } from "./password-prompt"

function terminal() {
  const input = Object.assign(new PassThrough(), { isTTY: true, setRawMode: () => input })
  // Like stderr in a real terminal: readline echoes input to a TTY output.
  const output = Object.assign(new PassThrough(), { isTTY: true, columns: 80 })
  let shown = ""
  output.on("data", (c) => (shown += String(c)))
  return { input, output, shown: () => shown }
}

describe("readPassword", () => {
  it("prompts on a terminal without echoing what is typed", async () => {
    const t = terminal()
    const pw = readPassword(t.input, t.output)
    t.input.write("correct horse battery\r")
    expect(await pw).toBe("correct horse battery")
    expect(t.shown()).toContain("Password (12+ chars): ")
    expect(t.shown()).not.toContain("correct")
    expect(t.shown()).not.toContain("*")
  })

  it("reads piped stdin whole", async () => {
    const input = new PassThrough()
    const pw = readPassword(input, new PassThrough())
    input.end("piped password value\n")
    expect(await pw).toBe("piped password value")
  })

  it("stops on Ctrl+C", async () => {
    const t = terminal()
    const pw = readPassword(t.input, t.output)
    t.input.write("\u0003")
    await expect(pw).rejects.toThrow(/cancelled/)
  })
})
