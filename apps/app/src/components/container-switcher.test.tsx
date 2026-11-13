import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"

import { ContainerSwitcher } from "@/components/container-switcher"

const containers = [
  { container: "default", memories: 5, documents: 1, lastActivity: 0 },
  { container: "work", memories: 2, documents: 0, lastActivity: 0 },
]

const agent = createAgent()

test("typing filters the containers and Enter picks the match", async () => {
  const onChange = vi.fn()
  render(<ContainerSwitcher value="default" containers={containers} onChange={onChange} />)
  await userEvent.click(screen.getByRole("button", { name: "Container: default. Change container" }))
  await userEvent.type(screen.getByRole("combobox", { name: "Find or type a container" }), "wor")
  expect(screen.queryByRole("option", { name: /default/ })).toBeNull()
  await userEvent.keyboard("{Enter}")
  expect(onChange).toHaveBeenCalledWith("work")
})

test("a new name offers to use it", async () => {
  const onChange = vi.fn()
  render(<ContainerSwitcher value="default" containers={containers} onChange={onChange} />)
  await userEvent.click(screen.getByRole("button", { name: "Container: default. Change container" }))
  await userEvent.type(screen.getByRole("combobox", { name: "Find or type a container" }), "travel")
  await userEvent.click(screen.getByRole("option", { name: "Use travel" }))
  expect(onChange).toHaveBeenCalledWith("travel")
})

test("an agent finds the switcher by name", () => {
  render(<ContainerSwitcher value="default" containers={containers} onChange={() => {}} />)
  const components = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  for (const entry of components) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(components.map((entry) => entry.label)).toContain("Container: default. Change container")
})
