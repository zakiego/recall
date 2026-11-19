// Connect: the exact config each agent client needs for this workspace, with
// the real origin, a key and the container from the header filled in. A key
// made here lives only in this page's state and is gone when you leave.
// Agent instructions (its own page) is the other way in: a prompt the agent
// runs to set itself up.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClientPicker, Code, ConnectPending, KeyCard, Note, Snippet, useCreatedKey } from "@/components/connect-parts"
import { PageHeader } from "@/components/page-header"
import { useContainer } from "@/hooks/use-container"
import { CLIENT_IDS, claudeCodeCommand, claudeDesktopConfig, genericMcpConfig, mcpUrl, restExamples, type ClientId } from "@/lib/connect"
import { getConnectInfo } from "@/lib/fns"

export const Route = createFileRoute("/_app/connect")({
  validateSearch: z.object({ client: z.enum(CLIENT_IDS).optional() }),
  loader: () => getConnectInfo(),
  pendingComponent: () => <ConnectPending label="Loading connect…" />,
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Connect · Recall" }] }),
  component: ConnectPage,
})

// Keys belong to one workspace. Keyed by it, the page drops a created key the
// moment you switch workspaces instead of pairing it with the new one.
function ConnectPage() {
  const { viewer } = Route.useRouteContext()
  return <ConnectBody key={viewer.workspace?.id ?? "none"} />
}

function ConnectBody() {
  const { origin, canManage } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const { client = "claude-code" } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const container = useContainer()
  const workspace = viewer.workspace?.name ?? "this workspace"
  const { created, setCreated, key, fresh } = useCreatedKey()

  const setClient = (next: ClientId) =>
    void navigate({ search: (prev: Record<string, unknown>) => ({ ...prev, client: next }), replace: true, resetScroll: false })

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        title="Connect"
        description={
          <>
            Hook an agent up to <span className="font-medium text-foreground">{workspace}</span>. Pick where it runs and copy the config.
          </>
        }
      />

      <KeyCard what="config" canManage={canManage} workspace={workspace} username={viewer.user.username} created={created} onCreated={setCreated} />

      <section aria-labelledby="connect-clients" className="flex min-w-0 flex-col gap-4">
        <ClientPicker id="connect-clients" title="Where does your agent run?" value={client} onChange={setClient} />

        <div className="flex flex-col gap-3 pt-1">
          {client === "claude-code" && (
            <>
              <Note>
                Run it in a terminal. Add <Code>-s user</Code> after <Code>add</Code> to use Recall in every project, then check it with{" "}
                <Code>claude mcp list</Code>.
              </Note>
              <Snippet title="Terminal" text={claudeCodeCommand(origin, key)} secret={key} fresh={fresh} />
            </>
          )}

          {client === "claude-desktop" && (
            <>
              <Note>
                Merge into <Code>mcpServers</Code>, then restart Claude Desktop. Needs Node.js.
              </Note>
              <Snippet title="claude_desktop_config.json" text={claudeDesktopConfig(origin, key)} secret={key} fresh={fresh} />
            </>
          )}

          {client === "mcp" && (
            <>
              <Note>Clients with a settings form take these three values.</Note>
              <Snippet title="Server URL" text={mcpUrl(origin)} />
              <Snippet title="Header name" text="Authorization" />
              <Snippet title="Header value" text={`Bearer ${key}`} secret={key} fresh={fresh} />
              <Note>
                Claude Code's <Code>.mcp.json</Code> and Cursor's <Code>mcp.json</Code> take this:
              </Note>
              <Snippet title=".mcp.json" text={genericMcpConfig(origin, key)} secret={key} fresh={fresh} />
            </>
          )}

          {client === "rest" && (
            <>
              <Note>
                Every <Code>/v1</Code> call takes the key as a Bearer token. These use the{" "}
                <Code>{container}</Code> container from the header.{" "}
                <a className="underline underline-offset-4 hover:text-foreground" href="/docs/api" target="_blank" rel="noreferrer">
                  API reference<span className="sr-only"> (opens in a new tab)</span>
                </a>
              </Note>
              <Snippet title="Shell" text={restExamples(origin, key, container)} secret={key} fresh={fresh} />
            </>
          )}
        </div>
      </section>

      <Note>
        Prefer the agent to do it?{" "}
        <Link to="/agent-instructions" search={{ c: container, client }} className="font-medium text-foreground underline underline-offset-4">
          Agent instructions
        </Link>
        .
      </Note>
    </div>
  )
}
