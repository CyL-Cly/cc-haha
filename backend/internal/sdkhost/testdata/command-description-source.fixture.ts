import { join, dirname } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'SDK command description source')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_DESCRIPTION_FIXTURE !== '1') throw new Error('Private pinned command projection fixture required')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/.claude/skills', '/Library/Application Support/ClaudeCode/.claude/agents', '/Library/Application Support/ClaudeCode/.claude/output-styles', '/usr/bin/rg', '/bin/rg', join(dirname(process.execPath), 'rg'), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: null }), clearLegacyApiKeyPrefetch() {}, startKeychainPrefetch() { throw new Error('No actual keychain') }, ensureKeychainPrefetchCompleted: async () => {} }))
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setOriginalCwd(home); state.setCwdState(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config'); config.enableConfigs()
const { formatDescriptionWithSource, getCommandName } = await import('../../../../src/commands')
const commands = input.commands.map((value: any) => ({ ...value, ...(typeof value.userFacingName === 'string' && value.userFacingName !== '' ? { userFacingName: () => value.userFacingName } : {}) }))
const control = commands.filter((command: any) => command.userInvocable !== false).map((command: any) => ({ name: getCommandName(command), description: formatDescriptionWithSource(command), argumentHint: command.argumentHint || '' }))
const system = commands.filter((command: any) => command.userInvocable !== false).map((command: any) => ({ name: command.name, description: command.description, ...(command.argumentHint ? { argumentHint: command.argumentHint } : {}) }))
if (capabilities.blocked.length) throw new Error('Source fixture capability denied: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ control, system, originalRows: input.commands, capabilities: { blocked: capabilities.blocked } }) + '\n')
