import { mock, spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { RetainedSkillsCase } from './retained-skills-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; rows: RetainedSkillsCase[]; probe?: string }
assertDisposableFixtureHome(input.home, 'Retained Skills')
if (Object.keys(input).some(name => !['home', 'rows', 'probe'].includes(name)) || !Array.isArray(input.rows) || input.rows.some(row => !['projection', 'history'].includes(row.kind) || typeof row.source !== 'string' || !['write', undefined].includes(row.tools)) || process.env.CC_HAHA_RETAINED_SKILLS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Bun.version !== '1.3.14') throw new Error('Retained Skills requires explicit private pinned profile')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
// Read's original result formatter resolves the active model before deciding
// whether to include its mitigation reminder. Only the absent external native
// credential-prefetch result is replaced; auth/model/formatter code stays real.
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
if (input.probe === 'native') Bun.spawn(['security'])
if (input.probe === 'network') await fetch('http://127.0.0.1:1')
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.home)
bootstrap.setOriginalCwd(input.home)
bootstrap.setProjectRoot(input.home)
bootstrap.setIsInteractive(false)
bootstrap.setSdkBetas([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
const actual = await import('../../../src/utils/messages')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
let trace: unknown[] = [], count = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => { const value = `skills-id-${++count}`; trace.push({ kind: 'uuid', value }); return value as ReturnType<typeof crypto.randomUUID> })
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]); if (!args.length) trace.push({ kind: 'date', value: this.toISOString() }) }
  static now() { return 1700000000000 }
} as DateConstructor
const observations: unknown[] = [], errorStacks: unknown[] = []
for (const row of input.rows) {
  count = 0
  trace = []
  const original = JSON.parse(row.source), before = JSON.stringify(original)
  let output: unknown = null, error: unknown = null, reordered: string[] | null = null
  try {
    if (row.kind === 'projection') output = actual.normalizeAttachmentForAPI(original)
    else {
      reordered = actual.reorderAttachmentsForAPI(original).map(message => JSON.stringify(message))
      output = actual.ensureToolResultPairing(actual.normalizeMessagesForAPI(original, row.tools === 'write' ? [FileWriteTool] : [], 'fixture-model'))
    }
  } catch (failure) {
    if (!(failure instanceof Error)) throw failure
    error = { class: failure.constructor.name, name: failure.name, message: failure.message, messageJSON: JSON.stringify(failure.message) }
    errorStacks.push({ name: row.name, stack: failure.stack })
  }
  observations.push({ name: row.name, before, after: JSON.stringify(original), reordered, output: output === null ? null : (output as unknown[]).map(message => JSON.stringify(message)), error, trace: [...trace] })
}
const { VERSION: sdkVersion } = await import('@anthropic-ai/sdk/version')
process.stdout.write(JSON.stringify({ observations, sourceOnly: { capabilities, errorStacks, sdk: sdkVersion, bun: Bun.version, revision: Bun.revision, profile: 'retained original payload; exported normalizer then actual pairing' } }))
