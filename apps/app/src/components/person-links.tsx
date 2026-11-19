import { Link } from "@tanstack/react-router"
import { UserRoundIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"

export function PersonLinks({ people, container }: { people: { id: string; name: string }[]; container: string }) {
  if (!people.length) return null
  return (
    <ul aria-label="People mentioned" className="flex min-w-0 flex-wrap items-center gap-1.5 pointer-coarse:gap-2">
      {people.map((p) => (
        <li key={p.id} className="min-w-0">
          <Badge
            variant="outline"
            className="max-w-48 pointer-coarse:h-7 pointer-coarse:px-2.5"
            title={p.name}
            render={<Link to="/people/$id" params={{ id: p.id }} search={{ c: container }} />}
          >
            <UserRoundIcon aria-hidden="true" />
            <span className="truncate">{p.name}</span>
          </Badge>
        </li>
      ))}
    </ul>
  )
}
