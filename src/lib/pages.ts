import { BrainIcon, KeyRoundIcon, MessageCircleQuestionMarkIcon, NetworkIcon, PlusIcon, SearchIcon, UserRoundIcon, UsersRoundIcon } from "lucide-react"

export const PAGES = [
  { to: "/", label: "Memories", Icon: BrainIcon, group: "Recall" },
  { to: "/search", label: "Search", Icon: SearchIcon, group: "Recall" },
  { to: "/ask", label: "Ask", Icon: MessageCircleQuestionMarkIcon, group: "Recall" },
  { to: "/graph", label: "Graph", Icon: NetworkIcon, group: "Recall" },
  { to: "/add", label: "Add", Icon: PlusIcon, group: "Recall" },
  { to: "/settings/members", label: "Members", Icon: UsersRoundIcon, group: "Workspace" },
  { to: "/settings/keys", label: "API keys", Icon: KeyRoundIcon, group: "Workspace" },
  { to: "/settings/account", label: "Account", Icon: UserRoundIcon, group: "You" },
] as const

export type PagePath = (typeof PAGES)[number]["to"]

// Settings pages aren't scoped to a container.
export function isSettings(pathname: string) {
  return pathname.startsWith("/settings")
}
