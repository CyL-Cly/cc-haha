import { assertDisposableFixtureHome } from './fixture-guard'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native preview files Source comparison')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.env.CLAUDE_CODE_LOCAL_VERSION = 'preview-fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
const { handlePreviewFs } = await import('../../../src/server/api/previewFs')
const { handleLocalFile } = await import('../../../src/server/api/localFile')
const { registerFilesystemAccessRoot } = await import('../../../src/server/services/filesystemAccessRoots')
for (const root of input.roots ?? []) registerFilesystemAccessRoot(root)
const observations = []
let current: any
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const url = new URL(request.url)
  return current.path.startsWith('/preview-fs/')
    ? await handlePreviewFs(url, async (id) => input.sessions[id] ?? null, request.headers)
    : await handleLocalFile(url, request.headers)
} })
try {
for (const row of input.cases) {
  current = row
  const response = await fetch(new URL(row.path, server.url), { headers: row.headers })
  observations.push({
    status: response.status,
    // Go net/http follows RFC 9110 and strips Content-Length from 204. Bun
    // emits an explicit zero. Compare the application headers and preserve
    // this transport-only exception here instead of hiding a status/body gap.
    headers: Object.fromEntries(['content-type', 'content-range', 'accept-ranges', 'content-length', 'cache-control', 'content-security-policy'].map((name) => [name, name === 'content-length' && response.status === 204 ? null : response.headers.get(name)])),
    body: Buffer.from(await response.arrayBuffer()).toString('base64'),
  })
}
} finally { server.stop(true) }
process.stdout.write(JSON.stringify(observations))
