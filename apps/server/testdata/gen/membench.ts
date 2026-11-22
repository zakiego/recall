// The TS side of tools/membench: RSS of a process running the real TS embedder
// (embed-model.ts, as embed-child.ts runs it) after load, one batch of 4 long
// texts and 20 batches. Run from apps/app: npx tsx ../server/testdata/gen/membench.ts
import { execFileSync } from "node:child_process"
import fs from "node:fs"
import { createEmbedder } from "../../../app/src/server/embed-model.ts" // embed-linux.sh rewrites this path

globalThis.fetch = async (input: unknown) => {
  throw new Error(`network access refused: ${String(input)}`)
}

// VmRSS on Linux (no ps in the slim image), ps elsewhere, like tools/membench.
const rss = () => {
  if (fs.existsSync("/proc/self/status")) {
    const line = fs.readFileSync("/proc/self/status", "utf8").split("\n").find((l) => l.startsWith("VmRSS:"))
    return Number(line!.split(/\s+/)[1]) / 1024
  }
  return Number(execFileSync("ps", ["-o", "rss=", "-p", String(process.pid)]).toString().trim()) / 1024
}

function longTexts() {
  const seeds = [
    "The memory engine stores facts about people places and plans so later questions find them quickly",
    "Pemerintah daerah mengumumkan jadwal baru untuk pelayanan publik di kantor kecamatan setiap hari kerja",
    "Alice is a software engineer based in Lisbon who likes Go TypeScript and self hosting",
    "Rapat koordinasi membahas jadwal rilis versi baru dan pembagian tugas tim secara rinci",
  ]
  return seeds.map((s) => {
    const words = s.split(" ")
    return Array.from({ length: 700 }, (_, i) => words[i % words.length]).join(" ")
  })
}

const embed = createEmbedder(process.argv[2] ?? "data/models")
console.log(`start rss=${rss().toFixed(1)} MiB`)
await embed(["warmup"], "query")
console.log(`loaded rss=${rss().toFixed(1)} MiB`)
const texts = longTexts()
await embed(texts, "passage")
console.log(`after 1 batch rss=${rss().toFixed(1)} MiB`)
for (let i = 1; i < 20; i++) await embed(texts, "passage")
console.log(`after 20 batches rss=${rss().toFixed(1)} MiB`)
