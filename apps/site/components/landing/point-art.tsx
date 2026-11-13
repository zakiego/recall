import { CircleCheckIcon, DatabaseIcon, HistoryIcon } from "lucide-react"

// Small drawn pieces of the product, one per bento cell, so each point shows
// what it means instead of saying it. Sample data matches the hero graph;
// the commands and tool names are the real ones from the docs.

export function HistoryArt() {
  return (
    <div className="lp-art-box lp-chain" aria-hidden="true">
      <div className="lp-chain-row" data-current="">
        <CircleCheckIcon className="lp-chain-icon lp-ok" />
        <span className="lp-chain-text">Runs in Puma shoes</span>
        <span className="lp-tag">
          v2<span className="lp-tag-word"> · current</span>
        </span>
      </div>
      <span className="lp-chain-link" />
      <div className="lp-chain-row">
        <HistoryIcon className="lp-chain-icon" />
        <span className="lp-chain-text">Runs in Adidas shoes</span>
        <span className="lp-tag">
          v1<span className="lp-tag-word"> · replaced</span>
        </span>
      </div>
    </div>
  )
}

const TOOLS = ["add_memory", "search_memories", "ask_memory", "get_profile"]

export function McpArt() {
  return (
    <div className="lp-art-box lp-term" aria-hidden="true">
      <p className="lp-term-line">
        <span className="lp-term-prompt">$</span> claude mcp add --transport http recall <span className="lp-term-dim">…/mcp</span>
      </p>
      <ul className="lp-tools">
        {TOOLS.map((t) => (
          <li key={t} className="lp-tool">
            {t}
          </li>
        ))}
        <li className="lp-tool lp-tool-more">+4</li>
      </ul>
    </div>
  )
}

export function HostArt() {
  return (
    <div className="lp-art-box lp-term" aria-hidden="true">
      <p className="lp-term-line">
        <span className="lp-term-prompt">$</span> docker compose up <span className="lp-term-dim">--wait</span>
      </p>
      <p className="lp-term-line">
        <CircleCheckIcon className="lp-term-icon lp-ok" /> recall-app-1 <span className="lp-ok">Healthy</span>
      </p>
      <p className="lp-term-file">
        <DatabaseIcon className="lp-term-icon" /> SQLite <span className="lp-term-dim">· sqlite-vec<span className="lp-term-extra"> · FTS5</span></span>
      </p>
    </div>
  )
}
