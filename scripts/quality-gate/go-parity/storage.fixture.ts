// Subprocess fixture: load the actual TS storage boundary only after the parent
// installs a disposable HOME/config environment. This is not a server bridge.
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

type Request = { operation: 'read' | 'update'; path: string; patch?: Record<string, unknown> }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Request
assertDisposableFixtureHome(process.env.HOME, 'Storage')
if (input.path !== join(process.env.CLAUDE_CONFIG_DIR ?? '', 'settings.json')) {
  throw new Error('Storage fixture requires the injected config settings path')
}
const { normalizeJsonObject, readRecoverableJsonFile } = await import('../../../src/server/services/recoverableJsonFile')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const warnings: string[] = []
console.warn = (...args: unknown[]) => { warnings.push(args.map(String).join(' ')) }
if (input.operation === 'update') {
  await new SettingsService().updateUserSettings(input.patch ?? {})
} else if (input.operation !== 'read') {
  throw new Error(`Unsupported storage fixture operation: ${input.operation}`)
}
const value = await readRecoverableJsonFile({
  filePath: input.path,
  label: 'settings',
  defaultValue: {},
  normalize: normalizeJsonObject,
})
console.log(JSON.stringify({ value, warnings }))
