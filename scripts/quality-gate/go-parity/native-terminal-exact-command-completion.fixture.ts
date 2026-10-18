import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original exact slash command completion')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_EXACT_COMPLETION_SOURCE !== '1') throw new Error('Pinned original completion Source required')
process.env.USER_TYPE = 'external'
process.env.ANTHROPIC_API_KEY = 'exact-completion-private-api-key'
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-completion-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', VERSION_CHANGELOG: '', FEEDBACK_CHANNEL: 'local' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(true)
state.setMainLoopModelOverride('claude-sonnet-4-6')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const status = (await import('../../../src/commands/status/index')).default
const statusline = (await import('../../../src/commands/statusline')).default
const { generateCommandSuggestions, applyCommandSuggestion } = await import('../../../src/utils/suggestions/commandSuggestions')
const results = []
for (const query of input.queries) {
  const commands = [statusline, status]
  const suggestions = generateCommandSuggestions(query, commands)
  let value = query, cursor = query.length
  const submits: any[] = []
  if (suggestions.length) applyCommandSuggestion(suggestions[0], true, commands, next => value = next, next => cursor = next, (...args) => submits.push(args))
  results.push({ query, suggestions: suggestions.map(row => ({ id: row.id, displayText: row.displayText, name: (row.metadata as any).name, type: (row.metadata as any).type })), value, cursor, submits })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original completion escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
