import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/")({
  component: Home,
})

function Home() {
  return (
    <main className="mx-auto flex min-h-svh max-w-xl flex-col justify-center gap-2 p-6">
      <h1 className="font-heading text-title">Recall</h1>
      <p className="text-muted-foreground">Memory engine. The dashboard lands in phase 6.</p>
    </main>
  )
}
