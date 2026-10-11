"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function SetupPage() {
  const [done, setDone] = useState(null);
  const [p1, setP1] = useState("");
  const [msg, setMsg] = useState("");
  useEffect(() => { api("GET", "/api/setup/status").then((r) => setDone(r.initialized)); }, []);
  async function submit(e) {
    e.preventDefault();
    try {
      await api("POST", "/api/setup", { password: p1 });
      window.location.href = "/login";
    } catch (err) { setMsg(String(err.message)); }
  }
  if (done === null) return <p style={{ textAlign: "center" }}>加载中…</p>;
  if (done) return (
    <div style={{ textAlign: "center", marginTop: "12vh" }}>
      <p>已完成初始化。</p>
      <a href="/login" style={{ color: "#8ab4ff" }}>前往登录</a>
    </div>
  );
  return (
    <div style={{ maxWidth: 360, margin: "12vh auto" }}>
      <h2>初始化管理员</h2>
      <p style={{ color: "#8a93a3" }}>设置 admin 密码（≥8 位），随后配置 LLM 即可开始。</p>
      <form onSubmit={submit}>
        <input type="password" value={p1} onChange={(e) => setP1(e.target.value)} placeholder="管理员密码" style={inputStyle} />
        <button style={btnStyle}>完成初始化</button>
        {msg && <p style={{ color: "#ff8a80" }}>{msg}</p>}
      </form>
    </div>
  );
}
const inputStyle = { width: "100%", padding: 10, margin: "8px 0", borderRadius: 6, border: "1px solid #2a3542", background: "#0f151d", color: "#e6e9ef", boxSizing: "border-box" };
const btnStyle = { width: "100%", padding: 10, marginTop: 8, borderRadius: 6, border: 0, background: "#2563eb", color: "#fff", cursor: "pointer" };
