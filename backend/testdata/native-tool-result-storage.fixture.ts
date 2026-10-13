import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Tool result storage Source')
if (process.env.CC_HAHA_TOOL_RESULT_SOURCE_FIXTURE !== '1') throw new Error('Missing tool output Source fixture')
const capabilities = await installClientFixtureCapabilities(home)
;(globalThis as any).MACRO = { VERSION: '1.2.3-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
;(await import('../../src/utils/config')).enableConfigs()
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setOriginalCwd(path.join(home, 'work'))
bootstrap.switchSession('11111111-1111-1111-1111-111111111111' as any)
bootstrap.setIsInteractive(false)
const source = await import('../../src/utils/toolResultStorage')
const directory = source.getToolResultsDir()
const observations = []
for (const row of input.rows) {
  fs.rmSync(directory, { recursive: true, force: true })
  fs.mkdirSync(path.dirname(directory), { recursive: true })
  if (row.failure) fs.writeFileSync(directory, 'blocking-file')
  if (row.existing !== undefined) {
    fs.mkdirSync(directory, { recursive: true })
    fs.writeFileSync(source.getToolResultPath(row.block.tool_use_id, Array.isArray(row.block.content)), row.existing)
  }
  const result = await source.processPreMappedToolResultBlock(row.block, row.name, row.threshold)
  const files = fs.existsSync(directory) && fs.statSync(directory).isDirectory()
    ? fs.readdirSync(directory).map(name => ({ name, bytes: Buffer.from(fs.readFileSync(path.join(directory, name))).toString('base64') }))
    : []
  observations.push({ result, files })
}
process.env.USER_TYPE = 'ant'
const growthbook = await import('../../src/services/analytics/growthbook')
const thresholds = input.thresholds.map((row:any) => {
  growthbook.setGrowthBookConfigOverride('tengu_satin_quoll', row.overrides)
  return source.getPersistenceThreshold(row.name, row.infinity ? Infinity : row.declared)
})
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ directory, observations, thresholds, blocked: capabilities.blocked }), () => resolve()))
