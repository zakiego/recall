"use client"

import {
  CircleCheckIcon,
  CirclePlusIcon,
  HistoryIcon,
  LinkIcon,
  type LucideIcon,
  MessageCircleIcon,
  PauseIcon,
  PlayIcon,
  ScanTextIcon,
  StickyNoteIcon,
} from "lucide-react"
import { memo, useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/cn"

// How Recall handles one message, as a graph like the dashboard's Graph page.
// Sample data. Adidas and Porto are already in memory; a chat says Alex
// switched to Puma; Recall lands the new fact and keeps the Adidas one,
// marked as replaced.
//
// Steps: 0 the chat flows in, 1 Recall reads it, 2 the new fact lands,
// 3 the old fact is marked replaced. Each step's caption types out first
// (a typewriter, so the eye knows where to look), then the graph acts, then
// it holds for `hold` ms. The page opens on an idle graph and the first line
// typing is what starts the story. Reduced motion shows the full text and
// step 3, the finished picture.
const STEPS: { icon: LucideIcon; caption: string; hold: number }[] = [
  { icon: MessageCircleIcon, caption: "A chat comes in: “I\u00A0switched to Puma shoes.”", hold: 1500 },
  { icon: ScanTextIcon, caption: "Recall reads it and pulls out the facts.", hold: 1200 },
  { icon: CirclePlusIcon, caption: "A new fact lands: runs in Puma shoes.", hold: 1400 },
  { icon: HistoryIcon, caption: "The Adidas fact isn’t deleted. It’s kept, marked as replaced.", hold: 3600 },
]
// Short names for the steps, for the rail under the graph.
const RAIL = ["Chat in", "Read", "New fact", "Kept"]
const CHAR_MS = 55
const LAST = STEPS.length - 1
const ICON_CLASS = "mr-2 inline size-5 -translate-y-px align-[-0.2em]"

function useSteps() {
  const ref = useRef<HTMLElement>(null)
  const [step, setStep] = useState(0)
  // Characters of the current caption typed so far. The server renders the
  // idle graph and every character hidden, so nothing has to be taken back on
  // load. Without JavaScript or with reduced motion, CSS shows the whole line
  // instead (see `.graph-char-hidden`).
  const [typed, setTyped] = useState(0)
  const [motionOk, setMotionOk] = useState(false)
  const [reduced, setReduced] = useState(false)
  const [paused, setPaused] = useState(false)
  const [onScreen, setOnScreen] = useState(true)
  const [pageVisible, setPageVisible] = useState(true)
  // Looping back from the finished graph to the first step: fade out, snap
  // to step 0 with transitions off, fade back in. Without this every
  // transition would play in reverse at once.
  const [rewinding, setRewinding] = useState(false)

  useEffect(() => {
    const query = window.matchMedia("(prefers-reduced-motion: reduce)")
    const sync = () => {
      setReduced(query.matches)
      setMotionOk(!query.matches)
    }
    sync()
    query.addEventListener("change", sync)
    return () => query.removeEventListener("change", sync)
  }, [])

  useEffect(() => {
    const onVisibility = () => setPageVisible(document.visibilityState === "visible")
    onVisibility()
    document.addEventListener("visibilitychange", onVisibility)
    const el = ref.current
    const observer = el ? new IntersectionObserver(([entry]) => setOnScreen(entry.isIntersecting)) : null
    if (el) observer?.observe(el)
    return () => {
      document.removeEventListener("visibilitychange", onVisibility)
      observer?.disconnect()
    }
  }, [])

  const running = motionOk && !paused && onScreen && pageVisible
  const length = STEPS[step].caption.length
  const typingDone = typed >= length

  // The typewriter: one character per tick while the replay runs.
  useEffect(() => {
    if (!running || rewinding || typingDone) return
    const timer = window.setTimeout(() => setTyped((t) => t + 1), CHAR_MS)
    return () => window.clearTimeout(timer)
  }, [running, rewinding, typingDone, typed])

  // Once the line is typed and the graph has acted, hold, then move on.
  useEffect(() => {
    if (!running || rewinding || !typingDone) return
    const timer = window.setTimeout(() => {
      if (step >= LAST) setRewinding(true)
      else {
        setStep(step + 1)
        setTyped(0)
      }
    }, STEPS[step].hold)
    return () => window.clearTimeout(timer)
  }, [running, rewinding, typingDone, step])

  useEffect(() => {
    if (!rewinding) return
    if (step !== 0) {
      // Wait out the fade, then jump while transitions are off. A stop
      // mid-fade jumps straight away so the graph never stays faded.
      const timer = window.setTimeout(
        () => {
          setStep(0)
          setTyped(0)
        },
        running ? 240 : 0,
      )
      return () => window.clearTimeout(timer)
    }
    // Step 0 has painted without transitions: turn them back on and fade in.
    let inner = 0
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => setRewinding(false))
    })
    return () => {
      cancelAnimationFrame(outer)
      cancelAnimationFrame(inner)
    }
  }, [rewinding, step, running])

  // The graph follows the text: while a line is typing it still shows the
  // step before (step 0's "before" is the idle graph, -1).
  const graphStep = reduced ? LAST : typingDone ? step : step - 1

  return {
    ref,
    step: reduced ? LAST : step,
    graphStep,
    typed: reduced ? STEPS[LAST].caption.length : typed,
    rewinding: motionOk && rewinding,
    running,
    motionOk,
    paused,
    togglePaused: () => setPaused((p) => !p),
  }
}

type Box = { x: number; y: number; w: number; h: number }
type Layout = {
  viewBox: string
  sources: Box[]
  recall: Box
  facts: Box[]
  inEdge: (i: number) => string
  outEdge: (i: number) => string
  version: string
  text: { source: number; recall: number; fact: number; note: number }
  icon: { source: number; fact: number; inset: number; gap: number }
  /** Width of the Recall mark plus its gap, and the label's rough width. */
  recallGroup: { mark: number; label: number }
  /** Radii of the rings around Recall, the mark's ring drawn large. */
  rings: number[]
}

// Wide: sources, Recall and facts left to right.
const WIDE: Layout = {
  viewBox: "0 0 1000 400",
  sources: [70, 200, 330].map((y) => ({ x: 40, y: y - 28, w: 140, h: 56 })),
  recall: { x: 396, y: 156, w: 200, h: 88 },
  facts: [90, 200, 310].map((y) => ({ x: 680, y: y - 34, w: 280, h: 68 })),
  inEdge: (i) => {
    const y = [70, 200, 330][i]
    return `M 180 ${y} C 290 ${y}, 300 200, 396 200`
  },
  outEdge: (i) => {
    const y = [90, 200, 310][i]
    return `M 596 200 C 645 200, 632 ${y}, 680 ${y}`
  },
  version: "M 960 90 C 1000 105, 1000 185, 960 200",
  text: { source: 20, recall: 28, fact: 20, note: 17 },
  icon: { source: 22, fact: 22, inset: 18, gap: 12 },
  recallGroup: { mark: 34, label: 84 },
  rings: [132, 188, 248],
}

// Narrow: sources on top, Recall in the middle, facts stacked below, sized so
// the caption and the whole graph fit in the first screen of a phone.
const NARROW: Layout = {
  viewBox: "0 0 360 340",
  sources: [62, 180, 298].map((x) => ({ x: x - 52, y: 2, w: 104, h: 40 })),
  recall: { x: 105, y: 92, w: 150, h: 50 },
  facts: [206, 260, 314].map((y) => ({ x: 44, y: y - 23, w: 270, h: 46 })),
  inEdge: (i) => {
    const x = [62, 180, 298][i]
    return `M ${x} 42 C ${x} 72, 180 64, 180 92`
  },
  outEdge: (i) => {
    const y = [206, 260, 314][i]
    // A spine down the left edge, so no edge crosses a fact.
    return `M 180 142 L 180 152 Q 180 162, 168 162 L 30 162 Q 18 162, 18 174 L 18 ${y - 12} Q 18 ${y}, 30 ${y} L 44 ${y}`
  },
  version: "M 314 206 C 350 217, 350 249, 314 260",
  text: { source: 15, recall: 19, fact: 16, note: 13 },
  icon: { source: 17, fact: 18, inset: 12, gap: 8 },
  recallGroup: { mark: 24, label: 58 },
  rings: [96, 140],
}

const SOURCES: { label: string; icon: LucideIcon }[] = [
  { label: "Chat", icon: MessageCircleIcon },
  { label: "Note", icon: StickyNoteIcon },
  { label: "Link", icon: LinkIcon },
]
const FACTS: { label: string; isNew?: boolean; replaced?: boolean }[] = [
  { label: "Runs in Puma shoes", isNew: true },
  { label: "Runs in Adidas shoes", replaced: true },
  { label: "Lives in Porto" },
]

function Edge({ d, drawn, flowing, ghost = true }: { d: string; drawn: boolean; flowing?: boolean; ghost?: boolean }) {
  return (
    <>
      {/* The grey track under the edge. An edge to a fact that hasn't landed
          yet has none, so there's no stub pointing at an empty slot. */}
      <path d={d} fill="none" strokeWidth={2} className={cn("graph-track stroke-border", !ghost && "opacity-0")} />
      <path
        d={d}
        fill="none"
        pathLength={1}
        strokeWidth={2.5}
        strokeLinecap="round"
        strokeDasharray={flowing ? "0.07 0.13" : "1 1"}
        strokeDashoffset={drawn ? 0 : 1}
        className={cn("graph-edge stroke-foreground", flowing && "graph-flow")}
      />
    </>
  )
}

// Memoised: while a line types, the graph's step doesn't change, so the SVG
// shouldn't re-render on every character.
const Diagram = memo(function Diagram({ layout, step, className }: { layout: Layout; step: number; className?: string }) {
  const { sources, recall, facts, text, icon } = layout
  const reading = step >= 1
  const landed = step >= 2
  const replaced = step >= 3

  // The Recall mark and label, centred together in the box.
  const groupWidth = layout.recallGroup.mark + layout.recallGroup.label
  const groupX = recall.x + (recall.w - groupWidth) / 2
  const markR = layout.recallGroup.mark * 0.3
  const markCx = groupX + markR + 2
  const cy = recall.y + recall.h / 2

  return (
    <svg viewBox={layout.viewBox} aria-hidden="true" className={cn("h-auto w-full overflow-visible", className)}>
      {/* Recall's mark (a ring and a dot) drawn large behind it. The rings
          brighten with the Recall box, from the moment it reads; every box
          sits on top of them. */}
      <g className={cn("graph-rings", reading && "graph-rings-on")} style={{ transformOrigin: `${recall.x + recall.w / 2}px ${cy}px` }}>
        {layout.rings.map((r, i) => (
          <circle
            key={r}
            cx={recall.x + recall.w / 2}
            cy={cy}
            r={r}
            fill="none"
            strokeWidth={1}
            strokeDasharray={i === 1 ? "2 6" : undefined}
            className="stroke-foreground"
          />
        ))}
      </g>
      {sources.map((_, i) => (
        <Edge key={i} d={layout.inEdge(i)} drawn={i === 0 && step >= 0} flowing={i === 0 && step === 0} />
      ))}
      {facts.map((_, i) => (
        <Edge key={i} d={layout.outEdge(i)} drawn={FACTS[i].isNew ? landed : true} ghost={FACTS[i].isNew ? landed : true} />
      ))}
      {/* The version edge: the new fact replaces the old one. */}
      <path
        d={layout.version}
        fill="none"
        pathLength={1}
        strokeWidth={2.5}
        strokeLinecap="round"
        strokeDasharray="1 1"
        strokeDashoffset={replaced ? 0 : 1}
        className="graph-edge stroke-success"
      />

      {sources.map((b, i) => {
        const Icon = SOURCES[i].icon
        const active = i === 0
        const tone = active ? "text-foreground" : "text-muted-foreground"
        return (
          <g key={SOURCES[i].label}>
            <rect
              x={b.x}
              y={b.y}
              width={b.w}
              height={b.h}
              rx={12}
              strokeWidth={1.5}
              className={cn("graph-node fill-card", active && step === 0 ? "stroke-foreground" : "stroke-border")}
            />
            <Icon
              x={b.x + icon.inset}
              y={b.y + (b.h - icon.source) / 2}
              width={icon.source}
              height={icon.source}
              className={tone}
            />
            <text
              x={b.x + icon.inset + icon.source + icon.gap}
              y={b.y + b.h / 2}
              dy="0.35em"
              fontSize={text.source}
              className={cn("font-medium", active ? "fill-foreground" : "fill-muted-foreground")}
            >
              {SOURCES[i].label}
            </text>
          </g>
        )
      })}

      <g>
        <rect
          x={recall.x}
          y={recall.y}
          width={recall.w}
          height={recall.h}
          rx={12}
          strokeWidth={1.5}
          className={cn("graph-node stroke-foreground", reading ? "fill-foreground" : "fill-card")}
        />
        {/* Recall's own mark: a ring and a dot. */}
        <g className={cn("graph-node-text", reading ? "text-background" : "text-foreground")}>
          <circle cx={markCx} cy={cy} r={markR} fill="none" stroke="currentColor" strokeWidth={markR * 0.42} />
          <circle cx={markCx} cy={cy} r={markR * 0.3} fill="currentColor" />
        </g>
        <text
          x={groupX + layout.recallGroup.mark}
          y={cy}
          dy="0.35em"
          fontSize={text.recall}
          className={cn("graph-node-text font-semibold", reading ? "fill-background" : "fill-foreground")}
        >
          Recall
        </text>
      </g>

      {facts.map((b, i) => {
        const fact = FACTS[i]
        const isNew = Boolean(fact.isNew)
        const isOld = Boolean(fact.replaced)
        const hidden = isNew && !landed
        const dim = isOld && replaced
        const textX = b.x + icon.inset + icon.fact + icon.gap
        return (
          <g key={fact.label} className={cn("graph-fact", hidden && "graph-fact-hidden")}>
            <rect
              x={b.x}
              y={b.y}
              width={b.w}
              height={b.h}
              rx={12}
              strokeWidth={1.5}
              className={cn("graph-node fill-card", isNew ? "stroke-success" : "stroke-border")}
            />
            {/* Current facts carry a check; the replaced one crossfades to a
                history icon when it's marked. */}
            <CircleCheckIcon
              x={b.x + icon.inset}
              y={b.y + (b.h - icon.fact) / 2}
              width={icon.fact}
              height={icon.fact}
              className={cn("graph-icon", isNew ? "text-success" : "text-muted-foreground", dim && "opacity-0")}
            />
            {isOld && (
              <HistoryIcon
                x={b.x + icon.inset}
                y={b.y + (b.h - icon.fact) / 2}
                width={icon.fact}
                height={icon.fact}
                className={cn("graph-icon text-success", !dim && "opacity-0")}
              />
            )}
            <text
              x={textX}
              y={b.y + b.h / 2}
              dy="0.35em"
              fontSize={text.fact}
              className={cn("graph-node graph-label", dim ? "graph-label-up fill-muted-foreground" : "fill-foreground")}
            >
              {fact.label}
            </text>
            {isOld && (
              <text
                x={textX}
                y={b.y + b.h / 2}
                dy="1.15em"
                fontSize={text.note}
                className={cn("graph-note fill-success-fg font-medium", !dim && "graph-note-hidden")}
              >
                replaced by Puma, kept
              </text>
            )}
          </g>
        )
      })}
    </svg>
  )
})

// One span per character, the same in the sizers and the typed line. The
// markup never changes while a line types, only which spans are hidden, so the
// balanced line breaks can't move and each tick is a paint, not a layout.
// (A typed text node next to a hidden rest rewrapped mid-line, and a CSS
// highlight over one text node typed choppily on iOS Safari.)
function Chars({ text, shown }: { text: string; shown: number }) {
  return Array.from(text, (char, i) => (
    <span key={i} className={i < shown ? undefined : "graph-char-hidden"}>
      {char}
    </span>
  ))
}

export function MemoryGraph() {
  const { ref, step, graphStep, typed, rewinding, running, motionOk, paused, togglePaused } = useSteps()
  const StepIcon = STEPS[step].icon
  const caption = STEPS[step].caption

  return (
    <figure
      ref={ref}
      data-running={running ? "true" : "false"}
      data-rewinding={rewinding ? "true" : undefined}
      className="flex flex-col gap-3 lg:gap-6"
    >
      {/* The caption comes first so it's read before the picture, and stays
          on screen with it on a phone. Screen readers get the whole story
          once, from the sr-only figcaption, instead of a line that types. */}
      <noscript>
        <style>{".graph-char-hidden{visibility:visible}"}</style>
      </noscript>
      <figcaption className="sr-only">
        An agent sends a chat saying Alex switched to Puma running shoes. Recall pulls out the facts, adds runs in Puma
        shoes, and keeps the older fact, runs in Adidas shoes, marked as replaced.
      </figcaption>
      <div className="flex items-center gap-3">
        {/* Keeps the caption centred against the button on the other side. */}
        <span aria-hidden="true" className="size-8 shrink-0" />
        {/* Every caption sits invisibly in the same grid cell, so the cell is
            as tall as the longest one at this width and nothing below moves
            while a line types or the next step starts. The whole block is
            hidden from screen readers; they get the figcaption above. */}
        <div aria-hidden="true" className="grid min-w-0 flex-1 text-center text-lg text-balance text-foreground">
          {STEPS.map(({ icon: Icon, caption: line }) => (
            <p key={line} className="invisible col-start-1 row-start-1 self-center">
              <Icon className={ICON_CLASS} />
              <Chars text={line} shown={line.length} />
            </p>
          ))}
          <p className="graph-stage col-start-1 row-start-1 self-center">
            <StepIcon className={cn(ICON_CLASS, "text-muted-foreground")} />
            <Chars key={step} text={caption} shown={typed} />
          </p>
        </div>
        {motionOk ? (
          <Button variant="ghost" size="icon" onClick={togglePaused} aria-label={paused ? "Play the animation" : "Pause the animation"}>
            {paused ? <PlayIcon /> : <PauseIcon />}
          </Button>
        ) : (
          <span aria-hidden="true" className="size-8 shrink-0" />
        )}
      </div>
      <Diagram layout={WIDE} step={graphStep} className="graph-stage hidden lg:block" />
      <Diagram layout={NARROW} step={graphStep} className="graph-stage mx-auto max-h-[46svh] max-w-sm md:max-h-none md:max-w-xl lg:hidden" />
      {/* Where the story is: one segment per step, filled as the graph acts. */}
      <ol aria-hidden="true" className="graph-rail">
        {RAIL.map((label, i) => (
          <li key={label} data-done={graphStep >= i ? "" : undefined} data-now={graphStep === i ? "" : undefined}>
            <span className="graph-rail-num">{String(i + 1).padStart(2, "0")}</span>
            {label}
          </li>
        ))}
      </ol>
    </figure>
  )
}
