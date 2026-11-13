import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import * as React from "react"
import { expect, test } from "vitest"

import { SimpleSelect } from "@/components/simple-select"

const zones = [
  { value: "UTC", label: "UTC" },
  { value: "Asia/Makassar", label: "Makassar (WITA)" },
  { value: "Asia/Jakarta", label: "Jakarta (WIB)" },
]

function TimeZone() {
  const [zone, setZone] = React.useState("Asia/Makassar")
  return (
    <>
      <SimpleSelect aria-label="Time zone" value={zone} options={zones} onChange={setZone} />
      <output aria-label="Picked zone">{zone}</output>
    </>
  )
}

const agent = createAgent()

test("the trigger shows the label, not the raw value", () => {
  render(<TimeZone />)
  expect(screen.getByRole("combobox", { name: "Time zone" }).textContent).toContain("Makassar (WITA)")
})

test("a person picks with the keyboard", async () => {
  render(<TimeZone />)
  screen.getByRole("combobox", { name: "Time zone" }).focus()
  await userEvent.keyboard("{Enter}")
  await userEvent.click(screen.getByRole("option", { name: "Jakarta (WIB)" }))
  expect(screen.getByRole("status", { name: "Picked zone" }).textContent).toBe("Asia/Jakarta")
})

test("an agent can find the select and pick a zone by its label", async () => {
  render(<TimeZone />)
  const components = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  for (const entry of components) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(components.map((entry) => entry.label)).toContain("Time zone")

  const result = await agent.act({ steps: [{ ref: "Time zone", action: "setValue", args: ["UTC"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  expect(screen.getByRole("status", { name: "Picked zone" }).textContent).toBe("UTC")
})
