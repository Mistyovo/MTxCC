"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function LLMPage() {
  const [profiles, setProfiles] = useState([]);
  const [form, setForm] = useState({ name: "", protocol: "openai", base_url: "", model: "", api_key: "" });
  const [msg, setMsg] = useState("");

  async function refresh() { try { setProfiles(await api("GET", "/api/llm/profiles")); } catch {} }
  useEffect(() => { refresh(); }, []);

  async function test(id) {
    setMsg("");
    try {
      const r = await api("POST", `/api/llm/profiles/${id}/test`);
      setMsg(`#${id} 测试：${r.ok ? "✅ 连通" : "❌ " + r.error}（${r.latency_ms}ms）`);
    } catch (e) { setMsg(e.message); }
  }

  return (
    <div>
      <h2>LLM 配置</h2>
      <p style={{ color: "#8a93a3" }}>故障转移链按 ID 顺序。Ollama 本地可填 base_url=http://host.docker.internal:11434/v1。</p>
      <table style={{ borderCollapse: "collapse", width: "100%", marginBottom: 24 }}>
        <thead><tr>{["ID", "名称", "协议", "模型", "状态", ""].map((h) => (
          <th key={h} style={{ textAlign: "left", padding: 8, borderBottom: "1px solid #1d2733", color: "#8a93a3" }}>{h}</th>
        ))}</tr></thead>
        <tbody>
          {profiles.map((p) => (
            <tr key={p.id}>
              <td style={td}>#{p.id}</td>
              <td style={td}>{p.name}</td>
              <td style={td}>{p.protocol}</td>
              <td style={td}>{p.model}</td>
              <td style={td}>{p.enabled ? "启用" : "停用"}</td>
              <td style={td}><button onClick={() => test(p.id)} style={smallBtn}>测试连接</button></td>
            </tr>
          ))}
        </tbody>
      </table>
      {msg && <p style={{ color: "#ffd479" }}>{msg}</p>}
    </div>
  );
}
const td = { padding: 8, borderBottom: "1px solid #141c26" };
const smallBtn = { padding: "4px 12px", borderRadius: 6, border: "1px solid #2a3542", background: "#151d27", color: "#8ab4ff", cursor: "pointer" };
