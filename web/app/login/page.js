"use client";
import { useState } from "react";
import { api, setToken } from "@/lib/api";

export default function LoginPage() {
  const [u, setU] = useState("admin");
  const [p, setP] = useState("");
  const [msg, setMsg] = useState("");
  async function login(e) {
    e.preventDefault();
    try {
      const r = await api("POST", "/api/auth/login", { username: u, password: p });
      setToken(r.token);
      window.location.href = "/tasks";
    } catch (err) { setMsg(String(err.message)); }
  }
  return (
    <div style={{ maxWidth: 360, margin: "12vh auto" }}>
      <h2>登录 MTxCC</h2>
      <form onSubmit={login}>
        <input value={u} onChange={(e) => setU(e.target.value)} placeholder="用户名" style={inputStyle} />
        <input type="password" value={p} onChange={(e) => setP(e.target.value)} placeholder="密码" style={inputStyle} />
        <button style={btnStyle}>登录</button>
        {msg && <p style={{ color: "#ff8a80" }}>{msg}</p>}
      </form>
      <p style={{ color: "#8a93a3" }}>首次使用请先 <a href="/setup" style={{ color: "#8ab4ff" }}>初始化管理员</a></p>
    </div>
  );
}
const inputStyle = { width: "100%", padding: 10, margin: "8px 0", borderRadius: 6, border: "1px solid #2a3542", background: "#0f151d", color: "#e6e9ef", boxSizing: "border-box" };
const btnStyle = { width: "100%", padding: 10, marginTop: 8, borderRadius: 6, border: 0, background: "#2563eb", color: "#fff", cursor: "pointer" };
