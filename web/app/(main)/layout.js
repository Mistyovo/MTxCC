"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function MainLayout({ children }) {
  const [noLLM, setNoLLM] = useState(false);
  useEffect(() => {
    api("GET", "/api/llm/profiles").then((ps) => setNoLLM(!ps || ps.length === 0)).catch(() => {});
  }, []);
  return (
    <div style={{ display: "flex", minHeight: "100vh" }}>
      <nav style={{ width: 200, padding: 16, borderRight: "1px solid #1d2733", background: "#0d1219" }}>
        <div style={{ fontWeight: 700, marginBottom: 24 }}>MTxCC</div>
        {[
          ["/tasks", "任务"],
          ["/llm", "LLM 配置"],
        ].map(([href, label]) => (
          <div key={href} style={{ marginBottom: 12 }}>
            <a href={href} style={{ color: "#8ab4ff", textDecoration: "none" }}>{label}</a>
          </div>
        ))}
      </nav>
      <main style={{ flex: 1, padding: 24 }}>
        {noLLM && (
          <div style={{ background: "#3a2b12", border: "1px solid #a1771f", borderRadius: 8, padding: 12, marginBottom: 16 }}>
            未配置 LLM，<a href="/llm" style={{ color: "#ffd479" }}>点此配置</a>
          </div>
        )}
        {children}
      </main>
    </div>
  );
}
