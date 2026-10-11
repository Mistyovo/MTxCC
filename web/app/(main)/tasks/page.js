"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

const defaultRoE = {
  mode: "enforce",
  in_scope: ["10.0.0.0/8", "192.168.0.0/16"],
  out_of_scope: [],
  forbidden_tlds: ["gov", "mil"],
};

export default function TasksPage() {
  const [tasks, setTasks] = useState([]);
  const [target, setTarget] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [roeText, setRoeText] = useState(JSON.stringify(defaultRoE, null, 2));
  const [msg, setMsg] = useState("");

  async function refresh() {
    try { setTasks(await api("GET", "/api/tasks")); } catch (e) { setMsg(e.message); }
  }
  useEffect(() => { refresh(); const t = setInterval(refresh, 3000); return () => clearInterval(t); }, []);

  async function create(e) {
    e.preventDefault();
    try {
      await api("POST", "/api/tasks", { target_text: target, timeout_seconds: 3600 });
      setTarget(""); setMsg(""); refresh();
    } catch (err) { setMsg(err.message); }
  }

  return (
    <div>
      <h2>任务</h2>
      <form onSubmit={create} style={{ marginBottom: 16 }}>
        <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="目标（如 http://10.0.0.5）"
          style={{ width: 480, padding: 10, borderRadius: 6, border: "1px solid #2a3542", background: "#0f151d", color: "#e6e9ef" }} />
        <button style={{ marginLeft: 8, padding: "10px 18px", borderRadius: 6, border: 0, background: "#2563eb", color: "#fff", cursor: "pointer" }}>创建并开始</button>
        <label style={{ marginLeft: 12, color: "#8a93a3" }}>
          <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} /> 高级（RoE JSON）
        </label>
        {advanced && (
          <textarea value={roeText} onChange={(e) => setRoeText(e.target.value)} rows={10}
            style={{ display: "block", width: 520, marginTop: 8, padding: 10, borderRadius: 6, border: "1px solid #2a3542", background: "#0f151d", color: "#e6e9ef", fontFamily: "monospace" }} />
        )}
        {msg && <p style={{ color: "#ff8a80" }}>{msg}</p>}
      </form>
      <table style={{ borderCollapse: "collapse", width: "100%" }}>
        <thead>
          <tr>{["ID", "目标", "状态", "创建时间", ""].map((h) => (
            <th key={h} style={{ textAlign: "left", padding: 8, borderBottom: "1px solid #1d2733", color: "#8a93a3" }}>{h}</th>
          ))}</tr>
        </thead>
        <tbody>
          {tasks.map((t) => (
            <tr key={t.id}>
              <td style={td}>#{t.id}</td>
              <td style={td}>{t.target_text}</td>
              <td style={td}><span style={badge(t.status)}>{t.status}</span></td>
              <td style={td}>{t.created_at}</td>
              <td style={td}><a href={`/tasks/detail?id=${t.id}`} style={{ color: "#8ab4ff" }}>详情</a></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
const td = { padding: 8, borderBottom: "1px solid #141c26" };
function badge(s) {
  const c = { running: "#1e3a5f", done: "#12401f", failed: "#4a1620", timeout: "#3a2b12", queued: "#26303b" }[s] || "#26303b";
  return { background: c, padding: "2px 10px", borderRadius: 99, fontSize: 12 };
}
