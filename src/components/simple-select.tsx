/**
 * A select from a flat list of value and label pairs.
 *
 * Every picker in this dashboard is that shape (projects, models, efforts,
 * statuses), and Base UI's select wants five components and a render function
 * to show a label instead of the raw value. Writing that out a dozen times is
 * how one of them ends up showing a project id instead of its slug.
 */

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { picked } from "@/lib/utils"

export interface Option {
  value: string
  label: string
}

export function SimpleSelect({
  id,
  value,
  options,
  onChange,
  className,
  size,
  disabled,
  "aria-label": ariaLabel,
}: {
  id?: string
  value: string
  options: Option[]
  onChange: (value: string) => void
  className?: string
  size?: "sm" | "default"
  disabled?: boolean
  "aria-label"?: string
}) {
  return (
    <Select value={value} onValueChange={picked(onChange)} disabled={disabled}>
      <SelectTrigger
        id={id}
        className={className}
        size={size}
        aria-label={ariaLabel}
      >
        <SelectValue>
          {(v: string) => options.find((o) => o.value === v)?.label ?? v}
        </SelectValue>
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}
