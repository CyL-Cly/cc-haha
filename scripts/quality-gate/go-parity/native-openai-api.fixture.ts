import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI API')
if (process.env.CC_HAHA_OPENAI_API_FIXTURE !== '1') throw new Error('Unowned OpenAI API fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { handleHahaOpenAIOAuthApi, handleHahaOpenAIOAuthCallback } = await import('../../../src/server/api/haha-openai-oauth')
const { hahaOpenAIOAuthService } = await import('../../../src/server/services/hahaOpenAIOAuthService')
const originalNow = Date.now
Date.now = () => input.now
const rows = []
try {
  for (const row of input.rows) {
    if (row.tokens !== undefined) {
      await mkdir(join(home, '.claude', 'cc-haha'), { recursive: true })
      await writeFile(hahaOpenAIOAuthService.getOAuthFilePath(), typeof row.tokens === 'string' ? row.tokens : JSON.stringify(row.tokens))
    }
    const url = new URL(row.path, 'http://localhost:3456')
    const req = new Request(url, {
      method: row.method,
      ...(row.method === 'GET' ? {} : { body: row.body ?? '' }),
    })
    const response = row.callback
      ? await handleHahaOpenAIOAuthCallback(url)
      : await handleHahaOpenAIOAuthApi(req, url, url.pathname.split('/').filter(Boolean))
    rows.push({ status: response.status, body: row.callback ? await response.text() : await response.json() })
  }
} finally {
  Date.now = originalNow
  hahaOpenAIOAuthService.dispose()
}
process.stdout.write(JSON.stringify(rows))
