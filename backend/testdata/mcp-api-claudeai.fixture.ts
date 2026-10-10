import { mock } from 'bun:test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Claude.ai desktop API Source')
if (process.env.CC_HAHA_CLAUDEAI_API_FIXTURE !== '1') throw new Error('Missing API fixture marker')
if (input.sortGroups) {
 process.stdout.write(JSON.stringify({locale:new Intl.Collator().resolvedOptions().locale,icu:process.versions.icu,groups:input.sortGroups.map((group:string[])=>group.toSorted((a,b)=>a.localeCompare(b)))}))
 process.exit(0)
}
const proxy = new URL(process.env.HTTPS_PROXY!)
if (proxy.protocol !== 'http:' || proxy.hostname !== '127.0.0.1') throw new Error('Unowned CONNECT proxy')
for (const [path, data] of Object.entries(input.files)) {
  if (!path.startsWith(home + '/')) throw new Error('Foreign fixture state')
  mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, data as string)
}
process.env.CLAUDE_CODE_LOCAL_VERSION = 'fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture'
await import('../../preload')
if (input.managedRoot) {
 if (!input.managedRoot.startsWith(home+'/')) throw new Error('Foreign managed-root primitive')
 const managedPath=await import('../../src/utils/settings/managedPath')
 mock.module('../../src/utils/settings/managedPath',()=>({...managedPath,getManagedFilePath:()=>input.managedRoot}))
}
const portable = await import('../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
mock.module('../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('private unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(input.cwd); state.setProjectRoot(input.cwd); state.setCwdState(input.cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
state.setIsInteractive(false)
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { resetSettingsCache } = await import('../../src/utils/settings/settingsCache')
resetSettingsCache()
const { handleMcpApi } = await import('../../src/server/api/mcp')
const results = []
for (const step of input.steps) {
  const url = new URL(step.path, 'http://127.0.0.1:3000')
  const response = await handleMcpApi(new Request(url, { method: step.method, headers: { 'content-type': 'application/json' }, ...(step.body ? { body: step.body } : {}) }), url, url.pathname.split('/').filter(Boolean))
  results.push({ status: response.status, body: await response.json() })
}
process.stdout.write(JSON.stringify(results))
