// The rollback direction for M1: password hashes and sealed connector values
// made by the Go build must work in the TS build. The Go tests write them when
// RECALL_M1_OUT and RECALL_M1_SEAL_OUT are set:
//
//   (apps/server) RECALL_M1_OUT=/tmp/pw.json RECALL_M1_SEAL_OUT=/tmp/seal.json go test -tags sqlite_fts5 -count=1 -run 'GoPasswordHashes|SealRoundTrip' ./internal/auth ./internal/connectors
//   (apps/app)    npx tsx ../server/testdata/gen/verify-go-m1.ts /tmp/pw.json /tmp/seal.json
import fs from "node:fs"
import { verifyLegacyPassword, verifyPassword } from "../../../app/src/server/auth.ts"
import { unseal } from "../../../app/src/server/connectors/crypto.ts"

const [pwFile, sealFile] = process.argv.slice(2)
if (!pwFile || !sealFile) throw new Error("usage: verify-go-m1.ts <passwords.json> <seal.json>")

let failures = 0
const pw = JSON.parse(fs.readFileSync(pwFile, "utf8")) as { password: string; scrypt: string; legacy: string }[]
for (const c of pw) {
  const ok =
    (await verifyPassword({ hash: c.scrypt, password: c.password })) &&
    !(await verifyPassword({ hash: c.scrypt, password: c.password + "x" })) &&
    (await verifyPassword({ hash: c.legacy, password: c.password })) &&
    (await verifyLegacyPassword(c.password, c.legacy)) &&
    !(await verifyPassword({ hash: c.legacy, password: c.password + "x" }))
  if (!ok) {
    failures++
    console.error(`password ${JSON.stringify(c.password)} failed`)
  }
}
const seal = JSON.parse(fs.readFileSync(sealFile, "utf8")) as { secret: string; cases: { plaintext: string; context: string; sealed: string }[] }
let opened = 0
for (const c of seal.cases) {
  if (c.plaintext === "") continue
  const got = unseal(seal.secret, c.sealed, c.context)
  if (got !== c.plaintext) {
    failures++
    console.error(`sealed value for ${JSON.stringify(c.context)} opened to ${JSON.stringify(got)}`)
  } else opened++
}
console.log(`${pw.length} Go password pairs, ${opened} Go sealed values checked in TS`)
if (failures) {
  console.error(`FAIL (${failures})`)
  process.exit(1)
}
console.log("PASS")
