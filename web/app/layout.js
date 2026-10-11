export const metadata = { title: "MTxCC · AI 自主渗透测试" };

export default function RootLayout({ children }) {
  return (
    <html lang="zh-CN">
      <body style={{ margin: 0, fontFamily: "system-ui, sans-serif", background: "#0b0f14", color: "#e6e9ef" }}>
        {children}
      </body>
    </html>
  );
}
