// One date on a fact: a precision choice (day, month, year) and the matching
// native input. The value is the short form ("2026-11-05", "2026-11", "2026"),
// or "" for no date.
import * as React from "react"
import { XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { parseDate, type DatePrecision } from "@/lib/dates"

export function precisionOf(value: string): DatePrecision {
  return parseDate(value)?.precision ?? "day"
}

// Keeps what it can when the precision changes: 2026-11-05 -> 2026-11 -> 2026.
function convert(value: string, to: DatePrecision) {
  const d = parseDate(value)
  if (!d) return ""
  if (to === "year") return d.date.slice(0, 4)
  if (to === "month") return d.date.slice(0, 7)
  return d.date
}

export function DateField({
  id,
  label,
  description,
  value,
  onChange,
  error,
}: {
  id: string
  label: string
  description?: string
  value: string
  onChange: (value: string) => void
  error?: string
}) {
  const [precision, setPrecision] = React.useState<DatePrecision>(() => precisionOf(value))
  // Follow the value when it is set from outside (another fact opened in the dialog).
  React.useEffect(() => {
    setPrecision((cur) => (value ? precisionOf(value) : cur))
  }, [value])

  const input =
    precision === "day" ? (
      <Input id={id} type="date" value={value} min="1900-01-01" max="2200-12-31" aria-invalid={!!error} onChange={(e) => onChange(e.target.value)} />
    ) : precision === "month" ? (
      <Input id={id} type="month" value={value} min="1900-01" max="2200-12" placeholder="YYYY-MM" aria-invalid={!!error} onChange={(e) => onChange(e.target.value)} />
    ) : (
      <Input
        id={id}
        type="text"
        inputMode="numeric"
        pattern="\d{4}"
        maxLength={4}
        placeholder="YYYY"
        value={value}
        aria-invalid={!!error}
        onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
      />
    )

  return (
    <Field data-invalid={!!error}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup
          aria-label={`${label} precision`}
          variant="outline"
          value={[precision]}
          onValueChange={(v: string[]) => {
            const next = v[0] as DatePrecision | undefined
            if (!next) return
            setPrecision(next)
            if (value) onChange(convert(value, next))
          }}
        >
          <ToggleGroupItem value="day">Day</ToggleGroupItem>
          <ToggleGroupItem value="month">Month</ToggleGroupItem>
          <ToggleGroupItem value="year">Year</ToggleGroupItem>
        </ToggleGroup>
        <div className="min-w-36 flex-1">{input}</div>
        {value && (
          <Button type="button" variant="ghost" size="icon" aria-label={`Clear ${label.toLowerCase()}`} onClick={() => onChange("")}>
            <XIcon />
          </Button>
        )}
      </div>
      {error ? <FieldError>{error}</FieldError> : description && <FieldDescription>{description}</FieldDescription>}
    </Field>
  )
}
