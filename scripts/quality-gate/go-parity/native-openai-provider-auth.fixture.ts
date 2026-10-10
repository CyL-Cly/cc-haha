import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'OpenAI provider auth status')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalNow = Date.now
Date.now = () => input.now
await import('../../../preload')
const { handleProvidersApi } = await import('../../../src/server/api/providers')
const { hahaOpenAIOAuthService } = await import('../../../src/server/services/hahaOpenAIOAuthService')
const directory = join(home, '.claude', 'cc-haha')
await mkdir(directory, { recursive: true })
await writeFile(join(directory, 'providers.json'), JSON.stringify({ schemaVersion: 6, providers: [], activeId: 'openai-official' }))
// Selected OAuth must never borrow unrelated Anthropic credentials.
process.env.ANTHROPIC_API_KEY = 'fake-unrelated-process-key'
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify({ env: { ANTHROPIC_AUTH_TOKEN: 'fake-unrelated-settings-token' } }))
const observations = []
try {
  for (const row of input.rows) {
    if (row.absent) await rm(hahaOpenAIOAuthService.getOAuthFilePath(), { force: true })
    else await writeFile(hahaOpenAIOAuthService.getOAuthFilePath(), JSON.stringify(row.tokens))
    const url = new URL('/api/providers/auth-status', 'http://localhost:3456')
    const response = await handleProvidersApi(new Request(url), url, url.pathname.split('/').filter(Boolean))
    observations.push({ status: response.status, body: await response.json() })
  }
} finally {
  Date.now = originalNow
  hahaOpenAIOAuthService.dispose()
}
process.stdout.write(JSON.stringify(observations))
