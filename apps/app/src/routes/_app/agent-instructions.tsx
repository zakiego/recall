// Agent instructions: a prompt you paste into the agent so it sets itself up.
// It connects to Recall, then adds the memory rules for the header's container
// to its own instructions. Connect (its own page) has the same config by hand.
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClientPicker, ConnectPending, KeyCard, Note, Snippet, useCreatedKey } from "@/components/connect-parts"
import { PageHeader } from "@/components/page-header"
import { useContainer } from "@/hooks/use-container"
import { CLIENT_IDS, clientLabel, KEY_PLACEHOLDER, setupPrompt, type ClientId } from "@/lib/connect"
import { getConnectInfo } from "@/lib/fns"

export const Route = createFileRoute("/_app/agent-instructions")({
  validateSearch: z.object({ client: z.enum(CLIENT_IDS).optional() }),
  loader: () => getConnectInfo(),
  pendingComponent: () => <ConnectPending label="Loading agent instructions" />,
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Agent instructions · Recall" }] }),
  component: InstructionsPage,
})

// Keyed by workspace for the same reason as Connect: a key never outlives it.
function InstructionsPage() {
  const { viewer } = Route.useRouteContext()
  return <InstructionsBody key={viewer.workspace?.id ?? "none"} />
}

function InstructionsBody() {
  const { origin, canManage } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const { client = "claude-code" } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const container = useContainer()
  const workspace = viewer.workspace?.name ?? "this workspace"
  const { created, setCreated, key, fresh } = useCreatedKey()
  const target = client === "rest" ? "your agent" : client === "mcp" ? "your MCP client" : clientLabel(client)

  const setClient = (next: ClientId) =>
    void navigate({ search: (prev: Record<string, unknown>) => ({ ...prev, client: next }), replace: true, resetScroll: false })

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        title="Agent instructions"
        description={
          <>
            A prompt you paste into the agent so it sets itself up: it connects to Recall, then adds the memory rules to its own instructions, for
            the <span className="font-mono text-caption break-all text-foreground">{container}</span> container. Switch the container in the header.
          </>
        }
      />

      <KeyCard what="setup prompt" canManage={canManage} workspace={workspace} username={viewer.user.username} created={created} onCreated={setCreated} />

      <section aria-labelledby="instructions-client" className="flex min-w-0 flex-col gap-4">
        <ClientPicker id="instructions-client" title="Which agent is it for?" value={client} onChange={setClient} />
      </section>

      <section aria-labelledby="instructions-prompt" className="flex min-w-0 flex-col gap-3">
        <h2 id="instructions-prompt" className="font-heading text-heading">
          Setup prompt
        </h2>
        <Note>Paste this into {target}. If it can't do a step itself, it tells you what to paste where.</Note>
        <Snippet title="Setup prompt" text={setupPrompt(client, origin, key, container)} secret={key} fresh={fresh} />
        <Note>
          {created
            ? "It carries your new key. Paste it into the agent's chat, not into a file. The rules it saves leave the key out."
            : canManage
              ? `It shows ${KEY_PLACEHOLDER} until you create a key above, and the agent stops to ask for one. The rules it saves never contain the key.`
              : `It shows ${KEY_PLACEHOLDER} until an admin gives you a key. The rules it saves never contain the key.`}
        </Note>
      </section>

      <Note>
        Prefer to do it by hand?{" "}
        <Link to="/connect" search={{ c: container, client }} className="font-medium text-foreground underline underline-offset-4">
          Connect
        </Link>{" "}
        has the config for each client.{created && " Copy your key first: it stays on this page only."}
      </Note>
    </div>
  )
}
