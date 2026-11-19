// The dates on a fact, as small badges: when it happened and when it is due.
// Due soon (within a week) or past due is status, so it uses the warning badge.
import { useRouteContext } from "@tanstack/react-router"
import { CalendarCheckIcon, CalendarClockIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { dated, daysBetween, formatDate, relativeDays } from "@/lib/dates"

export const DUE_SOON_DAYS = 7

type WithDates = { eventDate: string | null; eventPrecision: string | null; dueDate: string | null; duePrecision: string | null }

// The server works out "today" in the viewer's zone once per navigation, so the
// server render and the client agree and match the Upcoming page.
export function useToday() {
  return useRouteContext({ from: "/_app" }).today
}

// `due={false}` leaves the due date to the caller (Upcoming shows it in its own column).
export function FactDates({ memory, current = true, due: showDue = true }: { memory: WithDates; current?: boolean; due?: boolean }) {
  const today = useToday()
  const event = dated(memory.eventDate, memory.eventPrecision)
  const due = showDue ? dated(memory.dueDate, memory.duePrecision) : null
  if (!event && !due) return null
  const left = due ? daysBetween(today, due.date) : null
  const urgent = current && left !== null && left <= DUE_SOON_DAYS
  return (
    <>
      {event && (
        <Badge variant="outline">
          <CalendarCheckIcon />
          <span className="tabular-nums">Happened {formatDate(event)}</span>
        </Badge>
      )}
      {due && left !== null && (
        <Badge
          variant={urgent ? "warning" : "outline"}
          title={current ? `Due ${formatDate(due)}, ${relativeDays(left)}` : `Due ${formatDate(due)}`}
        >
          <CalendarClockIcon />
          <span className="tabular-nums">
            Due {formatDate(due)}
            {urgent && due.precision === "day" ? ` · ${relativeDays(left)}` : ""}
          </span>
        </Badge>
      )}
    </>
  )
}
