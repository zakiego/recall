import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"

// The request and the response shape are the API's own (README, "Waiting for
// facts"). Ids and dates are placeholders, and the hero says so.
const REQUEST = `curl -X POST "$RECALL/v1/memories?wait=1" \\
  -H "authorization: Bearer $RECALL_KEY" \\
  -H "content-type: application/json" \\
  -d '{"container":"demo",
       "content":"I switched to Puma running shoes."}'`

const RESPONSE = `{ "status": "done", "settled": true, "added": [],
  "updated": [{ "id": "mem_8f2k",
    "fact": "Switched to Puma running shoes",
    "kind": "dynamic", "supersedesId": "mem_3hq1" }],
  "invalidated": 0 }`

function Code({ children, label }: { children: string; label: string }) {
  return (
    <pre
      aria-label={label}
      tabIndex={0}
      className="overflow-x-auto rounded-lg bg-muted px-4 py-3 font-mono text-[0.8125rem] leading-relaxed text-foreground"
    >
      <code>{children}</code>
    </pre>
  )
}

export function HeroExchange() {
  return (
    <div className="chain-in flex min-w-0 flex-col gap-4">
      <Card className="min-w-0">
        <CardHeader>
          <CardTitle className="font-mono text-sm font-medium">POST /v1/memories?wait=1</CardTitle>
          <CardDescription>An agent tells Recall something new.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <Code label="Request">{REQUEST}</Code>
          <Code label="Response">{RESPONSE}</Code>
        </CardContent>
      </Card>

      <Card className="min-w-0" size="sm">
        <CardHeader>
          <CardTitle>Version chain in container demo</CardTitle>
          <CardDescription>The old fact isn&apos;t overwritten. It gets an end date and stays readable.</CardDescription>
        </CardHeader>
        <CardContent>
          <ol className="flex flex-col">
            <li className="flex flex-col gap-1.5 py-2.5">
              <div className="flex flex-wrap items-center gap-2">
                <Badge>current</Badge>
                <span className="font-mono text-xs text-muted-foreground tabular-nums">mem_8f2k · from 2026-11-09</span>
              </div>
              <p className="font-medium">Switched to Puma running shoes</p>
            </li>
            <Separator />
            <li className="flex flex-col gap-1.5 py-2.5 text-muted-foreground">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline">superseded</Badge>
                <span className="font-mono text-xs tabular-nums">mem_3hq1 · 2026-10-02 to 2026-11-09</span>
              </div>
              <p>Runs in Adidas running shoes</p>
            </li>
          </ol>
        </CardContent>
      </Card>
    </div>
  )
}
