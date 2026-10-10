import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'OpenAI refresh routing')
if (process.env.CC_HAHA_OPENAI_REFRESH_OPTIONS_FIXTURE !== '1') throw new Error('Unowned refresh routing fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const proxy = await import('../../../src/utils/proxy')
const results = []
const names = ['https_proxy', 'HTTPS_PROXY', 'http_proxy', 'HTTP_PROXY', 'no_proxy', 'NO_PROXY', 'ANTHROPIC_UNIX_SOCKET']
for (const row of rows) {
  proxy._resetKeepAliveForTesting()
  if (row.disabled) proxy.disableKeepAlive()
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(row.env)) process.env[name] = String(value)
  // The original client.ts calls this with exactly proxyUrl, no target/API flag.
  results.push(proxy.getProxyFetchOptions({ proxyUrl: row.override }))
}
process.stdout.write(JSON.stringify(results))
