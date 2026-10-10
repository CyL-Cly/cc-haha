import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native Ultracode')
if (process.env.CC_HAHA_ULTRACODE_FIXTURE !== '1') throw new Error('Missing Ultracode fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
process.chdir(home)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const settings = await import('../../../src/utils/settings/settings')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
const keyword = await import('../../../src/utils/workflows/keyword')
const attachments = await import('../../../src/utils/attachments')
const { normalizeAttachmentForAPI } = await import('../../../src/utils/messages')
const effort = await import('../../../src/commands/effort/effort')
const { canEnableUltracode } = await import('../../../src/utils/workflows/ultracode')
const { get3PModelCapabilityOverride } = await import('../../../src/utils/model/modelSupportOverrides')
const rows = []
for (const row of input.rows) {
  for (const [key, value] of Object.entries(row.environment ?? {})) {
    if (value === null) delete process.env[key]
    else process.env[key] = String(value)
  }
  fs.mkdirSync(path.join(home, '.claude'), { recursive: true })
  fs.writeFileSync(path.join(home, '.claude', 'settings.json'), JSON.stringify(row.settings ?? {}))
  resetSettingsCache()
  // Each row represents a fresh private Go process configuration. Source's
  // model capability memo assumes the process environment stays constant.
  get3PModelCapabilityOverride.cache.clear()
  state.setIsInteractive(row.interactive ?? true)
  if (row.kind === 'keyword') {
    const payload = attachments.getAttachmentsForTesting.workflowKeyword(row.text, { suppressed: row.suppressed ?? false })
    rows.push({ ranges: keyword.findKeywordRanges(row.text), payload, content: payload.flatMap(value => normalizeAttachmentForAPI(value as any)).map(value => value.message.content) })
  } else if (row.kind === 'transition') {
    const payload = attachments.getAttachmentsForTesting.ultracodeEffort(row.history, row.active)
    rows.push({ payload, content: payload.flatMap(value => normalizeAttachmentForAPI(value as any)).map(value => value.message.content) })
  } else if (row.kind === 'gate') {
    rows.push(canEnableUltracode(row.model))
  } else if (row.kind === 'effort') {
    const result = effort.executeEffort(row.args, row.model)
    rows.push({ result, settings: JSON.parse(fs.readFileSync(path.join(home, '.claude', 'settings.json'), 'utf8')) })
  } else throw new Error('Unknown actual Ultracode operation')
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
