import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import * as React from "react"
import { expect, test } from "vitest"

import { DateField } from "@/components/date-field"

function DueDate() {
  const [value, setValue] = React.useState("2026-11-16")
  return (
    <>
      <DateField id="due" label="Due date" description="When it's due." value={value} onChange={setValue} />
      <output aria-label="Saved date">{value}</output>
    </>
  )
}

const agent = createAgent()

test("the precision group keeps one choice pressed", async () => {
  render(<DueDate />)
  const day = screen.getByRole("button", { name: "Day" })
  expect(day.getAttribute("aria-pressed")).toBe("true")
  await userEvent.click(day)
  expect(day.getAttribute("aria-pressed")).toBe("true")
  await userEvent.click(screen.getByRole("button", { name: "Month" }))
  expect(screen.getByRole("status", { name: "Saved date" }).textContent).toBe("2026-11")
})

test("an agent finds the date and its precision by name and changes them", async () => {
  render(<DueDate />)
  const components = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  for (const entry of components) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = components.map((entry) => entry.label)
  expect(labels).toContain("Due date")
  expect(labels).toContain("Due date precision")

  const result = await agent.act({
    steps: [{ ref: "Due date precision", action: "setValue", args: [["year"]] }],
  })
  expect(result.ok, result.error?.message).toBe(true)
  expect(screen.getByRole("status", { name: "Saved date" }).textContent).toBe("2026")
})
