import {
  Box,
  CalendarClock,
  Clock,
  Wallet,
  BrainCircuit,
  Braces,
  Building,
  Cable,
  CircleCheck,
  Code,
  Container,
  Database,
  DatabaseBackup,
  Download,
  FileInput,
  FileText,
  GitBranch,
  Globe,
  HardDrive,
  Inbox,
  KeyRound,
  LayoutDashboard,
  Lightbulb,
  ListChecks,
  MessageCircleQuestionMark,
  PanelsTopLeft,
  Plug,
  Repeat,
  RotateCcw,
  Route,
  Rows3,
  Search,
  Server,
  SquareTerminal,
  Tag,
  TextQuote,
  Trash,
  UserRound,
  Users,
  Webhook,
} from "lucide-react"
import type { ReactNode } from "react"
import { Arrow, Diagram, Fork, Group, Join, Node } from "./primitives"

// The docs diagrams. Each one replaces an ASCII block. Labels stay short: the
// prose around each diagram carries the numbers and the reasons. The MDX
// children are the text description, read by screen readers and kept in the
// markdown export.

type Props = { children: ReactNode }

const mono = (text: string) => (
  <span translate="no" className="font-mono">
    {text}
  </span>
)

// Boxes in a row that sits side by side on a wide diagram: equal widths, titles on one top line.
const ROW_NODE = "@2xl:max-w-none @2xl:flex-1 @2xl:items-start"

export function ArchitectureDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={Globe} title="Request" detail="recall.zakiego.com/*" />
      <Arrow />
      <Node icon={Route} title="Cloudflare Worker" detail="apps/edge" />
      <Fork labels={["app paths", "everything else"]}>
        <Node stack icon={Cable} title="Cloudflare tunnel" />
        <Node stack icon={PanelsTopLeft} title="Cloudflare Pages" detail="apps/site" />
      </Fork>
      <div className="flex flex-col @2xl:flex-row">
        <div className="flex min-w-0 flex-col @2xl:w-[calc((100%-0.75rem)/2)] @2xl:shrink-0">
          <Arrow />
          <Group icon={Server} label="Your server (Docker host)">
            <Node icon={Container} title="Recall container" detail="apps/app" />
            <Arrow />
            <Node icon={HardDrive} title="Data volume" detail="/data, recall.db" />
          </Group>
        </div>
        <Arrow responsive grow label={mono("POST /v1/chat")} />
        <Node icon={BrainCircuit} title="LLM gateway" detail="extract, profile, ask" className="@2xl:mx-0 @2xl:w-44 @2xl:self-center" />
      </div>
    </Diagram>
  )
}

export function PipelineDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Join>
        {[
          <Node key="rest" stack icon={Code} title="REST" detail={<>POST /v1/<wbr />memories</>} />,
          <Node key="mcp" stack icon={Plug} title="MCP" detail="add_memory" />,
          <Node key="dash" stack icon={LayoutDashboard} title="Dashboard" detail="Add page" />,
        ]}
      </Join>
      <Node icon={Inbox} title="Document, queued" detail="documents + ingest job" />
      <Arrow />
      <Group icon={Repeat} label="Write path, in the job loop">
        <div className="flex flex-col @2xl:flex-row">
          <Node icon={FileInput} title="Ingest" detail="resolve, mask, chunk, embed" className={ROW_NODE} />
          <Arrow responsive />
          <Node icon={BrainCircuit} title="Extract" detail="one LLM call per window" className={ROW_NODE} />
          <Arrow responsive />
          <Node icon={UserRound} title="Profile" detail="rebuilt when facts change" className={ROW_NODE} />
        </div>
      </Group>
      <Arrow />
      <Node icon={Database} title="SQLite" detail="chunks, memories, FTS5, sqlite-vec" />
      <Fork>
        {[
          <Node key="search" stack icon={Search} title="Search" detail="hybrid, no LLM" />,
          <Node key="ask" stack icon={MessageCircleQuestionMark} title="Ask" detail="an ask job, one LLM call" />,
        ]}
      </Fork>
    </Diagram>
  )
}

export function ConceptsDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Group icon={Building} label="Workspace: the tenant boundary">
        <div className="grid grid-cols-2 gap-3">
          <Node stack icon={Users} title="Members" detail="owner, admin, member" />
          <Node stack icon={KeyRound} title="API keys" />
        </div>
        <Group icon={Box} label="Container: a person, project or team" className="mt-3">
          <div className="grid gap-3 @2xl:grid-cols-3 @2xl:items-start">
            <div className="flex flex-col">
              <Node icon={FileText} title="Documents" />
              <Arrow label="split into" />
              <Node icon={Rows3} title="Chunks" />
            </div>
            <Node icon={Lightbulb} title="Memories" detail="facts, with version chains" />
            <Node icon={UserRound} title="Profile" detail="static + dynamic summary" />
          </div>
        </Group>
      </Group>
    </Diagram>
  )
}

export function AskDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={MessageCircleQuestionMark} title="Question" />
      <Arrow />
      <Node icon={Search} title="Hybrid search" detail="no LLM" />
      <Fork join>
        {[
          <Node key="facts" stack icon={ListChecks} title="Facts" detail="m1 to m12" />,
          <Node key="chunks" stack optional icon={FileText} title="Chunks" detail="c1 to c6, optional" />,
          <Node key="profile" stack icon={UserRound} title="Profile" detail="not citable" />,
        ]}
      </Fork>
      <Node icon={BrainCircuit} title="One LLM call" detail="{ answer, covered }" />
      <Arrow />
      <Node icon={TextQuote} title="Citations" detail="[m3] → [1], made-up refs dropped" />
      <Arrow />
      <Node icon={Braces} title="Response" detail="answer, covered, citations, model, timingMs" />
    </Diagram>
  )
}

export function DeployDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={GitBranch} title="Push to main" />
      <Arrow />
      <Node icon={Webhook} title="GitHub webhook" detail="on your server" />
      <Arrow />
      <Group icon={SquareTerminal} label="Deploy script">
        <Node icon={Download} title="Pull" detail="git pull --ff-only" />
        <Arrow />
        <Node icon={DatabaseBackup} title="Snapshot" detail="scripts/pre-deploy.sh" />
        <Arrow />
        <Node icon={Tag} title="Tag the running image" detail=":prev" />
        <Arrow />
        <Node icon={Container} title="Build and start" detail="docker compose up --build --wait" />
        <Fork join labels={["healthy", "fails"]}>
          {[
            <Node key="live" stack icon={CircleCheck} title="New image runs" />,
            <Node key="back" stack icon={RotateCcw} title="Roll back" detail=":prev starts again" />,
          ]}
        </Fork>
        <Node icon={Trash} title="Prune old images" />
      </Group>
    </Diagram>
  )
}

export function AppSyncDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Join>
        {[
          <Node key="oslo" stack icon={ListChecks} title="Oslo" detail="GET /api/v1/changes" />,
          <Node key="fin" stack icon={Wallet} title="Finansial" detail="GET /api/v1/changes" />,
        ]}
      </Join>
      <Node icon={Clock} title="Sync, every hour or Sync now" detail="pages until hasMore is false, cursor per connector" />
      <Fork join labels={["Oslo", "Finansial"]}>
        {[
          <Node key="stage" stack icon={CalendarClock} title="Wait for the day to end" detail="app_connector_staging" />,
          <Node key="notable" stack icon={ListChecks} title="Keep notable items" detail="rules, loans, big transactions" />,
        ]}
      </Fork>
      <Node icon={Inbox} title="Documents" detail="oslo:day:<date>, fin:rule:<id>:<updatedAt>, fin:txn:<id>" />
      <Arrow />
      <Node icon={BrainCircuit} title="Ingest and extract" detail="the same job loop as every source" />
    </Diagram>
  )
}
