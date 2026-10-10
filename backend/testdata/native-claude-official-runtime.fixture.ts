import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { spyOn } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Claude official runtime')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../preload')
spyOn(console, 'warn').mockImplementation(() => {})
spyOn(console, 'error').mockImplementation(() => {})
Bun.plugin({ name: 'actual-claude-default-runtime', setup(build) {
  build.onLoad({ filter: /\/server\/ws\/handler\.ts$/ }, async args => ({
    contents: `${await Bun.file(args.path).text()}\nexport { getDefaultRuntimeSettings, resolveDefaultRuntimeModel, normalizeRuntimeOverrideInput, runtimeOverrides }`, loader: 'ts',
  }))
} })
const handler: any = await import('../../src/server/ws/handler')
const { ProviderService } = await import('../../src/server/services/providerService')
const { ConversationService } = await import('../../src/server/services/conversationService')
const { resolveClaudeOfficialRuntimeModel } = await import('../../src/server/services/claudeOfficialRuntime')
const directory = join(home, '.claude', 'cc-haha')
mkdirSync(directory, { recursive: true })
const observations = []
for (const row of input.rows) {
  writeFileSync(join(directory, 'providers.json'), JSON.stringify(row.index ?? { schemaVersion: 6, activeId: null, providers: [] }))
  writeFileSync(join(directory, 'settings.json'), JSON.stringify(row.managed ?? {}))
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(row.user ?? {}))
  if (row.tokens === null) rmSync(join(directory, 'oauth.json'), { force: true })
  else writeFileSync(join(directory, 'oauth.json'), JSON.stringify(row.tokens))
  const runtime = await handler.getDefaultRuntimeSettings()
  const model = await handler.resolveDefaultRuntimeModel(runtime)
  const resolved = await resolveClaudeOfficialRuntimeModel(row.configured)
  const replacement = await handler.normalizeRuntimeOverrideInput({ providerId: 'deleted', modelId: ' stale ', effortLevel: 'high' })
  let providerError: any
  try { await new ProviderService().getProvider('claude-official') } catch (error: any) { providerError = { status: error.statusCode, code: error.code, message: error.message } }
  const conversation: any = new ConversationService()
  const restoredInput = typeof row.configured === 'string' && row.configured ? row.configured : 'opus[1m]'
  handler.runtimeOverrides.set('private-claude-runtime', { providerId: null, modelId: restoredInput, effort: 'high' })
  const restored = await handler.getRuntimeSettings('private-claude-runtime')
  observations.push({ model, defaultModel: runtime.model ?? '', restoredModel: restored.model, restoredInput, effort: runtime.effort ?? '', resolved, replacement,
    providerError, officialEnv: await conversation.buildOfficialOAuthEnv(),
    modes: [undefined, null, 'saved'].map(id => ({ strip: conversation.shouldStripInheritedProviderEnv(id), mark: conversation.shouldMarkManagedOAuth(id) })) })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observations, capabilities }), () => resolve()))
process.exit(0)
