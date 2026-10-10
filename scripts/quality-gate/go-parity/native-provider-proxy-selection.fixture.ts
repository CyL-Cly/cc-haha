import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'

assertDisposableFixtureHome(process.env.HOME, 'Provider proxy saved selection')
globalThis.MACRO = { VERSION: 'proxy-fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
const { ProviderService } = await import('../../../src/server/services/providerService')
const root = `${process.env.CLAUDE_CONFIG_DIR}/cc-haha`
await mkdir(root, { recursive: true })
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const service = new ProviderService()
const output = []
for (const row of rows) {
  await writeFile(`${root}/providers.json`, JSON.stringify(row.index))
  try {
    output.push({ value: await service.getProviderForProxy(row.id) })
  } catch (error) {
    const e = error as { statusCode?: number; code?: string; message: string }
    output.push({ error: { statusCode: e.statusCode, code: e.code, message: e.message } })
  }
}
process.stdout.write(JSON.stringify(output))
