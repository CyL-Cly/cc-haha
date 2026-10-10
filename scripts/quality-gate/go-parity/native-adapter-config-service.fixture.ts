import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Native adapter config service')
if (process.env.CC_HAHA_ADAPTER_CONFIG_SERVICE_FIXTURE !== '1') throw new Error('Unowned adapter config fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
Bun.plugin({ name: 'actual-adapter-schema-export', setup(build) {
  build.onLoad({ filter: /\/server\/api\/adapters\.ts$/ }, async args => ({ contents: `${await Bun.file(args.path).text()}\nexport { parseAdapterConfigPatch }`, loader: 'ts' }))
} })
const { adapterService } = await import('../../../src/server/services/adapterService')
const { parseAdapterConfigPatch } = await import('../../../src/server/api/adapters') as any
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const output = []
for (const row of input.rows) {
  await writeFile(file, JSON.stringify(row.seed), { mode: 0o600 })
  try {
    let patch
    if (row.patch !== undefined) { patch = parseAdapterConfigPatch(row.patch); await adapterService.updateConfig(patch) }
    output.push({ patch, masked: await adapterService.getConfig(), raw: await adapterService.getRawConfig(), mode: (await stat(file)).mode & 0o777, stored: await readFile(file, 'utf8') })
  } catch (error: any) { output.push({ error: error.message, code: error.code, status: error.statusCode }) }
}
process.stdout.write(JSON.stringify(output))
