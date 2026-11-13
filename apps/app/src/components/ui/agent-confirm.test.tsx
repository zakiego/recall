import { render } from "@testing-library/react"
import { createAgent } from "@ui-sdk/react/agent"
import * as React from "react"
import { expect, test, vi } from "vitest"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogFooter,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

function RevokeKey({ onRevoke }: { onRevoke: () => void }) {
  const [open, setOpen] = React.useState(false)
  return (
    <>
      <Button onClick={() => setOpen(true)}>Revoke laptop</Button>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <AlertDialogTitle>Revoke “laptop”?</AlertDialogTitle>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              data-agent-confirm="Revokes this API key"
              onClick={() => {
                onRevoke()
                setOpen(false)
              }}
            >
              Revoke key
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

test("an agent needs a person to confirm a revoke", async () => {
  const onRevoke = vi.fn()
  render(<RevokeKey onRevoke={onRevoke} />)
  const open = await agent.act({ steps: [{ ref: "Revoke laptop", action: "press", args: [] }] })
  expect(open.ok, open.error?.message).toBe(true)

  const revoke = { steps: [{ ref: "Revoke key", action: "press", args: [] }] }
  expect((await agent.act(revoke)).ok).toBe(false)
  expect(onRevoke).not.toHaveBeenCalled()

  approve = true
  const done = await agent.act(revoke)
  expect(done.ok, done.error?.message).toBe(true)
  expect(onRevoke).toHaveBeenCalledTimes(1)
})
