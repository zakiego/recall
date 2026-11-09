import { BrainIcon, KeyRoundIcon, MessageCircleQuestionMarkIcon, NetworkIcon, PlusIcon, SearchIcon } from "lucide-react"

export const PAGES = [
  { to: "/", label: "Memories", Icon: BrainIcon, group: "Recall" },
  { to: "/search", label: "Search", Icon: SearchIcon, group: "Recall" },
  { to: "/ask", label: "Ask", Icon: MessageCircleQuestionMarkIcon, group: "Recall" },
  { to: "/graph", label: "Graph", Icon: NetworkIcon, group: "Recall" },
  { to: "/add", label: "Add", Icon: PlusIcon, group: "Recall" },
  { to: "/keys", label: "API keys", Icon: KeyRoundIcon, group: "Connect" },
] as const

export type PagePath = (typeof PAGES)[number]["to"]
