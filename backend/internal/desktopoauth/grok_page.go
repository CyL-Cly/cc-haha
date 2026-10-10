package desktopoauth

import "strings"

func GrokCallbackPage(success bool, message string) string {
	if success {
		return `<!doctype html><html><head><meta charset="utf-8"><title>Grok Login Success</title>
<style>body{font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#fafafa;color:#333}.card{text-align:center;padding:40px;background:white;border-radius:12px;box-shadow:0 4px 16px rgba(0,0,0,.06)}h1{color:#16a34a;margin:0 0 12px}p{color:#666}</style>
</head><body><div class="card"><h1>✓ Grok Login Successful</h1><p>Authorization is complete. You can close this window and return to Claude Code Haha.</p></div><script>setTimeout(() => window.close(), 3000)</script></body></html>`
	}
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(message)
	return `<!doctype html><html><head><meta charset="utf-8"><title>Grok Login Failed</title>
<style>body{font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#fafafa;color:#333}.card{text-align:center;padding:40px;background:white;border-radius:12px;box-shadow:0 4px 16px rgba(0,0,0,.06)}h1{color:#dc2626;margin:0 0 12px}pre{color:#666;white-space:pre-wrap;word-break:break-word;text-align:left;background:#f5f5f5;padding:12px;border-radius:6px}</style>
</head><body><div class="card"><h1>Grok Login Failed</h1><pre>` + escaped + `</pre></div></body></html>`
}
