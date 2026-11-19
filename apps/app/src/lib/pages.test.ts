import { expect, test } from "vitest"

import { isActivePage } from "@/lib/pages"

test("a page is active on its own path and its children only", () => {
  expect(isActivePage("/connectors", "/connectors")).toBe(true)
  expect(isActivePage("/connectors", "/connect")).toBe(false)
  expect(isActivePage("/people/42", "/people")).toBe(true)
  expect(isActivePage("/people", "/")).toBe(false)
  expect(isActivePage("/", "/")).toBe(true)
})
