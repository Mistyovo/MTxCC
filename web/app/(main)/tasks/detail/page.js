"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function TaskDetail() {
  const [id, setId] = useState(null);
  const [acts, setActs] = useState([]);
  const [graph, setGraph] = useState({ nodes: [], edges: [] });
  const [tab, setTab] = useState("stream");

  useEffect(() => {
    const tid = new URLSearchParams(window.location.search).get("id");
    setId(tid);
    if (!tid) return;
    const es = new EventSource(`/api/exploration/${tid}/activity/stream`);
    es.onmessage = (ev) => {
      try {
        const d = JSON.parse(ev.data);
        setActs((prev) => [...prev.slice(-200), d]);
      } catch {}
    };
    const g = setInterval(() => {
      api("GET", `/api/tasks/${tid}/graph`).then(setGraph).catch(() => {});
    }, 4000);
    return () => { es.close(); clearInterval(g); };
  }, []);

  return (
    <div>
      <h2>任务 #{id}</h2>
      <div style={{ marginBottom: 12 }}>
        {[["stream", "活动流"], ["graph", "探索图"]].map(([k, label]) => (
          <button key={k} onClick={() => setTab(k)}
            style={{ marginRight: 8, padding: "6px 14px", borderRadius: 6, border: tab === k ? 0 : "1px solid #2a3542", background: tab === k ? "#2563eb" : "transparent", color: "#fff", cursor: "pointer" }}>
            {label}
          </button>
        ))}
      </div>
      {tab === "stream" ? (
        <div style={{ fontFamily: "monospace", fontSize: 13, background: "#0d1219", borderRadius: 8, padding: 12, minHeight: 300, maxHeight: 480, overflow: "auto" }}>
          {acts.length === 0 && <p style={{ color: "#5a6472" }}>等待事件…</p>}
          {acts.map((a) => (
            <div key={a.id} style={{ padding: "4px 0", borderBottom: "1px solid #141c26", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
              <span style={{ color: "#5a8fd6" }}>#{a.id}</span>{" "}
              <span style={{ color: String(a.payload).includes("record_fact") ? "#7ee787" : "#8a93a3" }}>
                {String(a.payload).slice(0, 300)}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <GraphView graph={graph} />
      )}
    </div>
  );
}

// 轻量 SVG 图（无第三方依赖）。
function GraphView({ graph }) {
  const W = 900, H = 500;
  const nodes = graph.nodes || [];
  const pos = {};
  nodes.forEach((n, i) => {
    pos[n.id] = { x: 80 + (i % 6) * 140, y: 60 + Math.floor(i / 6) * 90 };
  });
  const colors = { goal: "#f0c674", intent: "#8ab4ff", fact: "#7ee787", finding: "#ff8a80" };
  return (
    <svg width={W} height={H} style={{ background: "#0d1219", borderRadius: 8 }}>
      {(graph.edges || []).map((e, i) => (
        <line key={i} x1={pos[e.source]?.x || 0} y1={pos[e.source]?.y || 0} x2={pos[e.target]?.x || 0} y2={pos[e.target]?.y || 0} stroke="#2a3542" strokeWidth={1.5} />
      ))}
      {nodes.map((n) => (
        <g key={n.id} transform={`translate(${pos[n.id]?.x || 0},${pos[n.id]?.y || 0})`}>
          <circle r={18} fill={colors[n.kind] || "#3a4552"} />
          <text x={24} y={5} fill="#8a93a3" fontSize={12}>{n.kind}:{n.state}</text>
          <text x={24} y={22} fill="#5a6472" fontSize={11}>{String(n.title).slice(0, 26)}</text>
        </g>
      ))}
    </svg>
  );
}
