import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'

assertDisposableFixtureHome(process.env.HOME, 'Provider proxy environment')
globalThis.MACRO = { VERSION: 'proxy-fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
const { ProviderService } = await import('../../../src/server/services/providerService')
const directory = `${process.env.CLAUDE_CONFIG_DIR}/cc-haha`
await mkdir(directory, { recursive: true })
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const service = new ProviderService()
const result = []
for (const row of rows) {
  ProviderService.setServerPort(row.port)
  await writeFile(`${directory}/providers.json`, JSON.stringify(row.index))
  await writeFile(`${directory}/settings.json`, JSON.stringify({ future: true, env: { RETAIN: 'owned', ANTHROPIC_API_KEY: 'old', ANTHROPIC_AUTH_TOKEN: 'old', ENABLE_TOOL_SEARCH: 'old' } }))
  const runtime = await service.getProviderRuntimeEnv('fixture')
  await service.activateProvider('fixture')
  result.push({ runtime, settings: await service.getManagedSettings() })
}
process.stdout.write(JSON.stringify(result))
