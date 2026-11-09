"use client"

import Image from "next/image"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

// Captured from a local build with sample data, at 1440 x 900. Each screen
// ships a light and a dark capture and the page shows the one that matches.
const SCREENS = [
  {
    id: "memories",
    label: "Memories",
    caption: "Current facts for a container. A fact that changed shows its versions, with dates and the document each came from.",
  },
  {
    id: "search",
    label: "Search",
    caption: "The same hybrid search agents get from /v1/search, with the score and source of every hit.",
  },
  {
    id: "ask",
    label: "Ask",
    caption: "A question answered from memory only. Each [n] links to the fact or excerpt it came from.",
  },
  {
    id: "graph",
    label: "Graph",
    caption: "Sources and the facts they produced. A line between two facts is a version: the newer one replaced the older.",
  },
] as const

export function Screens() {
  return (
    <Tabs defaultValue="memories" className="flex flex-col gap-4">
      <TabsList aria-label="Dashboard pages" className="self-start">
        {SCREENS.map((s) => (
          <TabsTrigger key={s.id} value={s.id}>
            {s.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {SCREENS.map((s) => (
        <TabsContent key={s.id} value={s.id} className="screen-in flex flex-col gap-3">
          <figure className="flex flex-col gap-3">
            <div className="overflow-hidden rounded-xl bg-card shadow-[var(--shadow-elevated)]">
              <Image
                src={`/screens/${s.id}-light.webp`}
                alt={`Recall dashboard, ${s.label} page`}
                width={1440}
                height={900}
                sizes="(min-width: 1280px) 1152px, 100vw"
                className="block h-auto w-full dark:hidden"
                priority={s.id === "memories"}
              />
              <Image
                src={`/screens/${s.id}-dark.webp`}
                alt={`Recall dashboard, ${s.label} page`}
                width={1440}
                height={900}
                sizes="(min-width: 1280px) 1152px, 100vw"
                className="hidden h-auto w-full dark:block"
              />
            </div>
            <figcaption className="max-w-prose text-sm text-muted-foreground">{s.caption}</figcaption>
          </figure>
        </TabsContent>
      ))}
    </Tabs>
  )
}
