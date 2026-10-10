import { join, dirname } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {
  absentManagedSettingsFixturePaths,
  installClientFixtureCapabilities,
} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual guide model request UTF16')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_GUIDE_UTF16_FIXTURE !== '1') {
  throw new Error('Owned pinned fixture required')
}
const capabilities = await installClientFixtureCapabilities(home, {
  loopbackFetchOrigin: new URL(process.env.ANTHROPIC_BASE_URL!).origin,
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
const { getCommands } = await import('../../../../src/commands')
const commands = await getCommands(input.cwd)
const { CLAUDE_CODE_GUIDE_AGENT } = await import('../../../../src/tools/AgentTool/built-in/claudeCodeGuideAgent')
const guide = CLAUDE_CODE_GUIDE_AGENT.getSystemPrompt({ toolUseContext: { options: { commands, agentDefinitions: { activeAgents: [] }, mcpClients: [] } } } as any)
const { getAnthropicClient } = await import('../../../../src/services/api/client')
const client = await getAnthropicClient({ maxRetries: 0, model: 'fixture-model', source: 'agent:claude-code-guide' })
await client.messages.create({ model: 'fixture-model', max_tokens: 1, messages: [{ role: 'user', content: 'Private guide request' }], system: [{ type: 'text', text: guide }] })
if (capabilities.blocked.length) throw new Error('Capability denied:' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ sourceGuideJSON: JSON.stringify(guide), loadedCommandJSON: JSON.stringify(commands.map(command => ({ name: command.name, description: command.description }))), capabilities: { blocked: capabilities.blocked } }) + '\n')
