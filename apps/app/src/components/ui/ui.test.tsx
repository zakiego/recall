import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import * as React from "react"
import { describe, expect, test, vi } from "vitest"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Toaster, toast } from "@/components/ui/toast"
import { Switch } from "@/components/ui/switch"
import { Toggle } from "@/components/ui/toggle"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Item } from "@/components/ui/item"
import { Separator } from "@/components/ui/separator"

describe("render wrappers", () => {
  test("Badge keeps its slot, variant and render element", () => {
    render(
      <Badge variant="success" render={<a href="/x" />}>
        Live
      </Badge>,
    )
    const badge = screen.getByRole("link", { name: "Live" })
    expect(badge.dataset.slot).toBe("badge")
    expect(badge.dataset.variant).toBe("success")
    expect(badge.className).toContain("bg-success-bg")
  })

  test("Item sets its slot, variant and size", () => {
    render(<Item variant="outline" size="sm">Row</Item>)
    const item = screen.getByText("Row")
    expect(item.dataset.slot).toBe("item")
    expect(item.dataset.variant).toBe("outline")
    expect(item.dataset.size).toBe("sm")
  })
})

describe("leaf controls", () => {
  test("Button keeps its variant classes and renders links", () => {
    render(
      <>
        <Button variant="outline">Save</Button>
        <Button render={<a href="/add" />}>Add content</Button>
      </>,
    )
    const save = screen.getByRole("button", { name: "Save" })
    expect(save.dataset.slot).toBe("button")
    expect(save.className).toContain("shadow-(--shadow-border)")
    expect(screen.getByRole("link", { name: "Add content" }).getAttribute("href")).toBe("/add")
  })

  test("InputGroupInput keeps the focus-within hook the ring CSS reads", () => {
    render(
      <InputGroup>
        <InputGroupInput aria-label="Filter facts" />
      </InputGroup>,
    )
    const input = screen.getByRole("textbox", { name: "Filter facts" })
    expect(input.hasAttribute("data-focus-within")).toBe(true)
  })

  test("Separator carries its orientation for the data-vertical styles", () => {
    render(<Separator orientation="vertical" />)
    const line = screen.getByRole("separator")
    expect(line.getAttribute("data-orientation")).toBe("vertical")
  })
})

describe("form controls", () => {
  test("Switch toggles with Space and reports a boolean", async () => {
    const onChange = vi.fn()
    function Demo() {
      const [on, setOn] = React.useState(false)
      return <Switch aria-label="Show history" checked={on} onCheckedChange={(v) => { setOn(v); onChange(v) }} />
    }
    render(<Demo />)
    const sw = screen.getByRole("switch", { name: "Show history" })
    expect(sw.hasAttribute("data-unchecked")).toBe(true)
    sw.focus()
    await userEvent.keyboard(" ")
    expect(onChange).toHaveBeenLastCalledWith(true)
    expect(sw.hasAttribute("data-checked")).toBe(true)
  })

  test("Checkbox maps indeterminate to a mixed state", async () => {
    const onChange = vi.fn()
    render(<Checkbox aria-label="Select all messages" checked={false} indeterminate onCheckedChange={onChange} />)
    const box = screen.getByRole("checkbox", { name: "Select all messages" })
    expect(box.getAttribute("aria-checked")).toBe("mixed")
    box.focus()
    await userEvent.keyboard(" ")
    expect(onChange.mock.calls[0][0]).toBe(true)
  })

  test("RadioGroup moves with arrow keys", async () => {
    const onChange = vi.fn()
    render(
      <RadioGroup aria-label="Window" value="7" onValueChange={onChange}>
        <RadioGroupItem value="7" aria-label="7 days" />
        <RadioGroupItem value="30" aria-label="30 days" />
      </RadioGroup>,
    )
    screen.getByRole("radio", { name: "7 days" }).focus()
    await userEvent.keyboard("{ArrowDown}")
    expect(onChange.mock.calls[0][0]).toBe("30")
  })

  test("Toggle and ToggleGroup press with Space", async () => {
    const onPressed = vi.fn()
    const onValue = vi.fn()
    render(
      <>
        <Toggle aria-label="Needs review" pressed={false} onPressedChange={onPressed}>Review</Toggle>
        <ToggleGroup aria-label="Kind" value={["all"]} onValueChange={onValue}>
          <ToggleGroupItem value="all">All</ToggleGroupItem>
          <ToggleGroupItem value="static">Static</ToggleGroupItem>
        </ToggleGroup>
      </>,
    )
    screen.getByRole("button", { name: "Needs review" }).focus()
    await userEvent.keyboard(" ")
    expect(onPressed.mock.calls[0][0]).toBe(true)
    await userEvent.click(screen.getByRole("button", { name: "Static" }))
    expect(onValue.mock.calls[0][0]).toEqual(["static"])
  })

  test("a single-choice ToggleGroup keeps its pressed item", async () => {
    const onValue = vi.fn()
    render(
      <ToggleGroup aria-label="Sort" defaultValue={["recent"]} onValueChange={onValue}>
        <ToggleGroupItem value="recent">Recent</ToggleGroupItem>
        <ToggleGroupItem value="name">Name</ToggleGroupItem>
      </ToggleGroup>,
    )
    const recent = screen.getByRole("button", { name: "Recent" })
    await userEvent.click(recent)
    expect(recent.getAttribute("aria-pressed")).toBe("true")
    expect(onValue).not.toHaveBeenCalled()
  })
})

describe("tabs and progress", () => {
  test("Tabs select with arrow keys and style the selected tab", async () => {
    render(
      <Tabs defaultValue="text">
        <TabsList aria-label="Content type">
          <TabsTrigger value="text">Text</TabsTrigger>
          <TabsTrigger value="url">URL</TabsTrigger>
        </TabsList>
        <TabsContent value="text">Paste text</TabsContent>
        <TabsContent value="url">Paste a link</TabsContent>
      </Tabs>,
    )
    const text = screen.getByRole("tab", { name: "Text" })
    expect(text.hasAttribute("data-selected")).toBe(true)
    text.focus()
    await userEvent.keyboard("{ArrowRight}")
    expect(screen.getByRole("tab", { name: "URL" }).hasAttribute("data-selected")).toBe(true)
    expect(screen.getByRole("tabpanel").textContent).toBe("Paste a link")
  })

  test("Progress fills its indicator from the value", () => {
    const { container } = render(<Progress aria-label="Relevance" value={40} />)
    const fill = container.querySelector<HTMLElement>("[data-slot=progress-indicator]")
    expect(fill?.style.width).toBe("40%")
  })
})

describe("popups", () => {
  test("Tooltip describes a named trigger", () => {
    render(
      <Tooltip>
        <TooltipTrigger render={<Button aria-label="Switch to dark theme" />} />
        <TooltipContent>Switch to dark theme</TooltipContent>
      </Tooltip>,
    )
    const trigger = screen.getByRole("button", { name: "Switch to dark theme" })
    const id = trigger.getAttribute("aria-describedby") ?? trigger.getAttribute("aria-labelledby")
    expect(id && document.getElementById(id)?.textContent).toContain("Switch to dark theme")
  })

  test("Popover opens from its trigger and closes on Escape with focus back", async () => {
    render(
      <Popover>
        <PopoverTrigger render={<Button aria-label="Change container" />} />
        <PopoverContent aria-label="Containers">
          <Button>Pick default</Button>
        </PopoverContent>
      </Popover>,
    )
    const trigger = screen.getByRole("button", { name: "Change container" })
    await userEvent.click(trigger)
    expect(trigger.getAttribute("aria-expanded")).toBe("true")
    expect(screen.getByRole("button", { name: "Pick default" })).toBeTruthy()
    await userEvent.keyboard("{Escape}")
    expect(trigger.getAttribute("aria-expanded")).toBe("false")
    expect(document.activeElement).toBe(trigger)
  })
})

describe("dropdown menu", () => {
  test("items run onSelect from the keyboard and focus returns to the trigger", async () => {
    const onEdit = vi.fn()
    const onDelete = vi.fn()
    render(
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button aria-label="Actions for Renew the domain" />} />
        <DropdownMenuContent>
          <DropdownMenuItem onSelect={onEdit}>Edit</DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onSelect={onDelete}>Delete</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )
    const trigger = screen.getByRole("button", { name: "Actions for Renew the domain" })
    trigger.focus()
    await userEvent.keyboard("{Enter}")
    const remove = screen.getByRole("menuitem", { name: "Delete" })
    expect(remove.hasAttribute("data-destructive")).toBe(true)
    await userEvent.keyboard("{ArrowDown}{Enter}")
    expect(onDelete).toHaveBeenCalledTimes(1)
    expect(onEdit).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(trigger)
  })

  test("radio items report the picked value", async () => {
    const onValue = vi.fn()
    render(
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button aria-label="Workspace" />} />
        <DropdownMenuContent>
          <DropdownMenuRadioGroup value="a" onValueChange={onValue}>
            <DropdownMenuRadioItem value="a">Alpha</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="b">Beta</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    )
    await userEvent.click(screen.getByRole("button", { name: "Workspace" }))
    expect(screen.getByRole("menuitemradio", { name: "Alpha" }).getAttribute("aria-checked")).toBe("true")
    await userEvent.click(screen.getByRole("menuitemradio", { name: "Beta" }))
    expect(onValue.mock.calls[0][0]).toBe("b")
  })
})

describe("dialogs", () => {
  test("Dialog is named by its title and its close button returns focus", async () => {
    render(
      <Dialog>
        <DialogTrigger render={<Button>New key</Button>} />
        <DialogContent>
          <DialogTitle>New API key</DialogTitle>
        </DialogContent>
      </Dialog>,
    )
    const trigger = screen.getByRole("button", { name: "New key" })
    await userEvent.click(trigger)
    expect(screen.getByRole("dialog", { name: "New API key" })).toBeTruthy()
    await userEvent.click(screen.getByRole("button", { name: "Close" }))
    expect(screen.queryByRole("dialog")).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  test("AlertDialog cancel closes it and the action stays the page's", async () => {
    const onOpenChange = vi.fn()
    const onRevoke = vi.fn()
    render(
      <AlertDialog open onOpenChange={onOpenChange}>
        <AlertDialogContent>
          <AlertDialogTitle>Revoke the key?</AlertDialogTitle>
          <AlertDialogCancel>Keep it</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={onRevoke}>Revoke key</AlertDialogAction>
        </AlertDialogContent>
      </AlertDialog>,
    )
    expect(screen.getByRole("alertdialog", { name: "Revoke the key?" })).toBeTruthy()
    await userEvent.click(screen.getByRole("button", { name: "Revoke key" }))
    expect(onRevoke).toHaveBeenCalledTimes(1)
    expect(onOpenChange).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole("button", { name: "Keep it" }))
    expect(onOpenChange.mock.calls[0][0]).toBe(false)
  })
})

describe("toast", () => {
  test("toast.add shows the title, description and a close button", async () => {
    render(<Toaster />)
    act(() => {
      toast.add({ title: "Revoked laptop", description: "It stops working now.", type: "success" })
    })
    expect(await screen.findByText("Revoked laptop")).toBeTruthy()
    expect(screen.getByText("It stops working now.")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Close toast" })).toBeTruthy()
    expect(document.querySelector("[data-slot=toast]")?.getAttribute("data-type")).toBe("success")
  })
})
