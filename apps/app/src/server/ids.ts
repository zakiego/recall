import { randomBytes } from "node:crypto"

const ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz"

// Prefixed, sortable-enough random ids: mem_8f3k..., doc_..., chk_...
export function newId(prefix: "mem" | "doc" | "chk" | "key" | "usr" | "acc" | "mbr" | "ws" | "inv" | "con"): string {
  const bytes = randomBytes(16)
  let out = ""
  for (const b of bytes) out += ALPHABET[b & 31]
  return `${prefix}_${out}`
}
