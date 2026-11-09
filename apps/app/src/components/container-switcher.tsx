// Every page is scoped to one container. This picks it: the known ones with
// their fact counts, or any new name typed in (a container exists as soon as
// something is added to it).
import * as React from "react"
import { CheckIcon, ChevronsUpDownIcon, PlusIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"

export type ContainerInfo = { container: string; memories: number; documents: number; lastActivity: number }

export function ContainerSwitcher({
  value,
  containers,
  onChange,
}: {
  value: string
  containers: ContainerInfo[]
  onChange: (container: string) => void
}) {
  const [open, setOpen] = React.useState(false)
  const [query, setQuery] = React.useState("")
  const list = containers.some((c) => c.container === value)
    ? containers
    : [{ container: value, memories: 0, documents: 0, lastActivity: 0 }, ...containers]
  const typed = query.trim()
  const canCreate = typed.length > 0 && typed.length <= 128 && !list.some((c) => c.container === typed)

  const pick = (c: string) => {
    onChange(c)
    setOpen(false)
    setQuery("")
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button variant="outline" size="sm" className="max-w-[min(18rem,45vw)] justify-between gap-2" aria-label={`Container: ${value}. Change container`}>
            <span className="text-muted-foreground max-sm:hidden">Container</span>
            <span className="truncate font-mono text-caption" title={value}>
              {value}
            </span>
            <ChevronsUpDownIcon aria-hidden="true" className="text-muted-foreground" />
          </Button>
        }
      />
      <PopoverContent align="end" className="w-80 p-0">
        <Command>
          <CommandInput placeholder="Find or type a new container" value={query} onValueChange={setQuery} />
          <CommandList>
            <CommandEmpty>No container by that name.</CommandEmpty>
            <CommandGroup heading="Containers">
              {list.map((c) => (
                <CommandItem key={c.container} value={c.container} onSelect={() => pick(c.container)} className="gap-2">
                  <CheckIcon aria-hidden="true" className={cn("shrink-0", c.container === value ? "opacity-100" : "opacity-0")} />
                  <span className="line-clamp-2 min-w-0 flex-1 font-mono text-caption break-all" title={c.container}>
                    {c.container}
                  </span>
                  <span className="shrink-0 text-caption text-muted-foreground tabular-nums">{plural(c.memories, "fact")}</span>
                </CommandItem>
              ))}
            </CommandGroup>
            {canCreate && (
              <CommandGroup heading="New">
                <CommandItem value={`__new__${typed}`} onSelect={() => pick(typed)} className="gap-2">
                  <PlusIcon aria-hidden="true" />
                  <span className="min-w-0 truncate">
                    Use <span className="font-mono">{typed}</span>
                  </span>
                </CommandItem>
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
