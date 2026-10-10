package cliauth

const openAILoginSuccessHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>cc-haha OpenAI Authorization Successful</title>
    <style>
      body { font-family: system-ui, -apple-system, sans-serif; display:flex; justify-content:center; align-items:center; height:100vh; margin:0; background:#131010; color:#f1ecec; }
      .container { text-align:center; padding:2rem; }
      p { color:#b7b1b1; }
    </style>
  </head>
  <body>
    <div class="container">
      <h1>Authorization Successful</h1>
      <p>You can close this window and return to Claude Code Haha.</p>
    </div>
    <script>setTimeout(() => window.close(), 2000)</script>
  </body>
</html>`
const openAILoginErrorHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>cc-haha OpenAI Authorization Failed</title>
  </head>
  <body>
    <h1>Authorization Failed</h1>
    <p>You can close this window and return to Claude Code Haha.</p>
  </body>
</html>`
