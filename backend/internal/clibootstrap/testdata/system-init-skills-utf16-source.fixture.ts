import { join, dirname } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {
  absentManagedSettingsFixturePaths,
  installClientFixtureCapabilities,
} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual system-init skills UTF16')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SKILLS_UTF16_FIXTURE !== '1') {
  throw new Error('Owned pinned fixture required')
}
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: [
    ...absentManagedSettingsFixturePaths(),
    '/Library/Application Support/ClaudeCode/.claude/skills',
    '/Library/Application Support/ClaudeCode/.claude/agents',
    '/Library/Application Support/ClaudeCode/.claude/output-styles',
    '/usr/bin/rg',
    '/bin/rg',
    join(dirname(process.execPath), 'rg'),
    '/home/claude/.claude/remote/.api_key',
    '/home/claude/.claude/remote/.oauth_token',
  ],
})
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({
  getLegacyApiKeyPrefetchResult: () => ({ stdout: null }),
  clearLegacyApiKeyPrefetch() {},
  startKeychainPrefetch() { throw new Error('No keychain') },
  ensureKeychainPrefetchCompleted: async () => {},
}))
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config')
config.enableConfigs()
if (typeof input.cwd !== 'string' || !input.cwd.startsWith(home + '/')) throw new Error('Owned cwd required')
if (!Array.isArray(input.directories) || input.directories.some((path: string) => !path.startsWith(home + '/'))) throw new Error('Owned plugin directories required')
state.setOriginalCwd(input.cwd)
state.setCwdState(input.cwd)
state.setProjectRoot(input.cwd)
state.setInlinePlugins(input.directories)
const { initBundledSkills } = await import('../../../../src/skills/bundled/index')
initBundledSkills()
const { getCommands, getSlashCommandToolSkills } = await import('../../../../src/commands')
const { buildSystemInitMessage } = await import('../../../../src/utils/messages/systemInit')
const commands = await getCommands(input.cwd)
const skills = await getSlashCommandToolSkills(input.cwd)
const original = buildSystemInitMessage({ tools: [], mcpClients: [], model: 'fixture-model', permissionMode: 'bypassPermissions', commands, agents: [], skills, plugins: [], fastMode: false })
const sourceJSON = JSON.stringify(original)
const source = JSON.parse(sourceJSON)
const go = JSON.parse(input.goInitJSON)
const units = (value: string) => Array.from({ length: value.length }, (_, i) => value.charCodeAt(i))
if (capabilities.blocked.length) throw new Error('Capability denied:' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ sourceInitJSON: sourceJSON, goInitJSON: input.goInitJSON, sourceSkillsUnits: source.skills.map(units), goSkillsUnits: go.skills.map(units), capabilities: { blocked: capabilities.blocked } }) + '\n')
