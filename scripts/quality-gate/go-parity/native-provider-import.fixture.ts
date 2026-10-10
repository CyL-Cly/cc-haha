import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original provider batch import')
globalThis.MACRO = { VERSION: 'import-fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
await import('../../../preload')
const { ProviderService } = await import('../../../src/server/services/providerService')
const dir = `${process.env.CLAUDE_CONFIG_DIR}/cc-haha`
await mkdir(dir, { recursive: true })
const output = []
const service = new ProviderService()
for (const row of input.cases) {
  await writeFile(`${dir}/providers.json`, JSON.stringify(row.index))
  const imported = row.single ? [await service.addProvider(row.inputs[0])] : await service.importProviders(row.inputs)
  const bytes = await readFile(`${dir}/providers.json`, 'utf8')
  let value = JSON.stringify({ imported, persisted: JSON.parse(bytes) })
  imported.forEach((provider, index) => { value = value.split(provider.id).join(`new-${index}`) })
  output.push(JSON.parse(value))
}
process.stdout.write(JSON.stringify(output))
