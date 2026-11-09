// A document's processing state as a word, a tone and an icon. Never color alone.
import { CircleCheckIcon, CircleDashedIcon, CircleXIcon, LoaderCircleIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"

export type DocStatus = "queued" | "processing" | "done" | "failed"

const LOOK: Record<DocStatus, { tone: "success" | "soft" | "destructive" | "outline"; Icon: typeof CircleCheckIcon; label: string }> = {
  queued: { tone: "outline", Icon: CircleDashedIcon, label: "Queued" },
  processing: { tone: "soft", Icon: LoaderCircleIcon, label: "Processing" },
  done: { tone: "success", Icon: CircleCheckIcon, label: "Done" },
  failed: { tone: "destructive", Icon: CircleXIcon, label: "Failed" },
}

export function StatusBadge({ status }: { status: DocStatus }) {
  const { tone, Icon, label } = LOOK[status]
  return (
    // Keyed by status so a change remounts it and the new state fades in.
    <Badge key={status} variant={tone} className="transition-[opacity,scale] duration-150 ease-emphasized starting:scale-96 starting:opacity-0">
      <Icon aria-hidden="true" className={status === "processing" ? "motion-safe:animate-spin" : undefined} />
      {label}
    </Badge>
  )
}
