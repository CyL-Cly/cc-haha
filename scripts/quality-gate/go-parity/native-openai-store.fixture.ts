import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'

assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI store')
if (process.env.CC_HAHA_OPENAI_STORE_FIXTURE !== '1') throw new Error('Unowned OpenAI store fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { HahaOpenAIOAuthService } = await import('../../../src/server/services/hahaOpenAIOAuthService')
const originalNow = Date.now
Date.now = () => input.now
const rows = []
try {
  for (const row of input.rows) {
    const service = new HahaOpenAIOAuthService()
    const file = service.getOAuthFilePath()
    await mkdir(dirname(file), { recursive: true })
    await rm(file, { force: true })
    if (row.tokens !== undefined) {
      await writeFile(file, JSON.stringify(row.tokens), { mode: 0o600 })
    }
    const calls: string[] = []
    service.setRefreshFn(async (token) => {
      calls.push(token)
      if (row.refreshError) throw new Error(row.refreshError)
      return row.response
    })
    try {
      const value = await service.ensureFreshTokens()
      rows.push({
        value,
        calls,
        stored: await readFile(file, 'utf8').catch(() => null),
        mode: await stat(file).then(info => info.mode & 0o777).catch(() => null),
      })
    } finally {
      service.dispose()
    }
  }
  process.stdout.write(JSON.stringify(rows))
} finally {
  Date.now = originalNow
}
