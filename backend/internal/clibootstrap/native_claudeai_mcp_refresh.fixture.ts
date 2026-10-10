import { mock } from 'bun:test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual Claude.ai MCP refresh Source')
if (process.env.CC_HAHA_CLAUDEAI_REFRESH_FIXTURE !== '1') throw new Error('Missing refresh marker')
const proxy = new URL(process.env.HTTPS_PROXY!)
if (proxy.hostname !== '127.0.0.1' || proxy.protocol !== 'http:') throw new Error('Unowned token network primitive')
const config = join(home, '.claude'), credentials = join(config, '.credentials.json')
mkdirSync(config, { recursive: true })
writeFileSync(credentials, JSON.stringify(input.credentials))
writeFileSync(join(config, '.claude.json'), JSON.stringify(input.global))
writeFileSync(join(config, 'settings.json'), '{}')
Date.now = () => 1000000
process.env.CLAUDE_CODE_LOCAL_VERSION = 'fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture'
await import('../../../preload')
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const files = await import('../../../src/utils/execFileNoThrow')
const execa = await import('execa')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('private unavailable Keychain') } }))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...files, async execFileNoThrow(file: string, ...args: any[]) {
  if (file === 'security') return { code: 1, stdout: '', stderr: 'private unavailable Keychain' }
  return files.execFileNoThrow(file, ...args)
} }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home); state.setCwdState(home); state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
resetSettingsCache()
const auth = await import('../../../src/utils/auth')
if (input.replacement) {
  auth.getClaudeAIOAuthTokens()
  writeFileSync(credentials, JSON.stringify(input.replacement))
}
const values = await Promise.all(Array.from({ length: input.concurrent }, () => input.action === '401' ? auth.handleOAuth401Error(input.sent) : auth.checkAndRefreshOAuthTokenIfNeeded()))
process.stdout.write(JSON.stringify({ values, credentials: JSON.parse(readFileSync(credentials, 'utf8')) }) + '\n')
