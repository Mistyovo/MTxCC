"use client";
// API 封装：token 存 localStorage；401 跳登录。
export function token() { return typeof window !== "undefined" ? localStorage.getItem("mtx_token") || "" : ""; }
export function setToken(t) { localStorage.setItem("mtx_token", t); }

export async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + token() },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401 && typeof window !== "undefined" && !path.includes("login")) {
    window.location.href = "/login";
    throw new Error("未授权");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}
