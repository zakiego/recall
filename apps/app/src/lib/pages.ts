import { BellIcon, BookUserIcon, BotIcon, BrainIcon, CableIcon, CalendarClockIcon, KeyRoundIcon, MessageCircleQuestionMarkIcon, NetworkIcon, PlugIcon, PlusIcon, SearchIcon, UserRoundIcon, UsersRoundIcon } from "lucide-react"

export const PAGES = [
  { to: "/", label: "Memories", Icon: BrainIcon, group: "Recall" },
  { to: "/people", label: "People", Icon: BookUserIcon, group: "Recall" },
  { to: "/upcoming", label: "Upcoming", Icon: CalendarClockIcon, group: "Recall" },
  { to: "/search", label: "Search", Icon: SearchIcon, group: "Recall" },
  { to: "/ask", label: "Ask", Icon: MessageCircleQuestionMarkIcon, group: "Recall" },
  { to: "/graph", label: "Graph", Icon: NetworkIcon, group: "Recall" },
  { to: "/add", label: "Add", Icon: PlusIcon, group: "Recall" },
  { to: "/connectors", label: "Connectors", Icon: PlugIcon, group: "Recall" },
  { to: "/connect", label: "Connect", Icon: CableIcon, group: "Connect" },
  { to: "/agent-instructions", label: "Agent instructions", Icon: BotIcon, group: "Connect" },
  { to: "/settings/keys", label: "API keys", Icon: KeyRoundIcon, group: "Connect" },
  { to: "/settings/members", label: "Members", Icon: UsersRoundIcon, group: "Workspace" },
  { to: "/settings/account", label: "Account", Icon: UserRoundIcon, group: "You" },
  { to: "/settings/reminders", label: "Reminders", Icon: BellIcon, group: "You" },
] as const

export type PagePath = (typeof PAGES)[number]["to"]

// Settings pages aren't scoped to a container.
export function isSettings(pathname: string) {
  return pathname.startsWith("/settings")
}
