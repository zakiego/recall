/**
 * A select from a flat list of value and label pairs.
 *
 * Every picker in this dashboard is that shape (projects, models, efforts,
 * statuses). Passing the options as `items` lets the trigger show the label
 * before the list has ever opened, instead of the raw value.
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
    <Select items={options} value={value} onValueChange={picked(onChange)} disabled={disabled}>
      <SelectTrigger
        id={id}
        className={className}
        size={size}
        aria-label={ariaLabel}
      >
        <SelectValue />
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
