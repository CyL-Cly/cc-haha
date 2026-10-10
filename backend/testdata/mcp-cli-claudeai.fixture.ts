import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
const home = assertDisposableFixtureHome(process.env.HOME, 'Public Claude.ai CLI Source')
if (process.env.CC_HAHA_CLAUDEAI_CLI_FIXTURE !== '1') throw new Error('Missing CLI marker')
const api = new URL(process.env.HTTPS_PROXY!)
if (api.hostname !== '127.0.0.1' || api.protocol !== 'http:') throw new Error('Unowned OAuth HTTP endpoint')
process.env.CLAUDE_CODE_LOCAL_VERSION = 'fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture'
await import('../../preload')
const portable = await import('../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
mock.module('../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('private unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(home); state.setProjectRoot(home); state.setCwdState(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { resetSettingsCache } = await import('../../src/utils/settings/settingsCache')
resetSettingsCache()
const { mcpListHandler } = await import('../../src/cli/handlers/mcp')
await mcpListHandler()
