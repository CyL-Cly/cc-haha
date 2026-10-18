import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual local Claude credential check')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CLAUDE_LOCAL_STAT_FIXTURE !== '1') throw new Error('Pinned private Source required')
const caps = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'], loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-local-stat-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-credentials')
  try { return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) } catch { return null }
} }) }))
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source OS primitive')
  return null
} }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getAnthropicClient } = await import('../../../src/services/api/client')
const path = join(home, '.claude', '.credentials.json')
const outcomes: any[] = []
for (const step of input.steps) {
  if (step.token === null) fs.rmSync(path, { force: true })
  else {
    fs.writeFileSync(path, JSON.stringify({ claudeAiOauth: step.token }))
    fs.utimesSync(path, step.mtime, step.mtime)
  }
  const client = await getAnthropicClient({ maxRetries: 0, model: 'claude-sonnet-4-5', source: 'repl_main_thread' })
  await client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: step.name }] })
  outcomes.push(step.name)
}
if (caps.blocked.length) throw new Error('Source escaped private boundary')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ outcomes, blocked: caps.blocked, secureReads: caps.secureReads }), () => resolve()))
process.exit(0)
