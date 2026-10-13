import fs from 'node:fs'
import { join } from 'node:path'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK permission mode producer')
if (process.env.CC_HAHA_SDK_MODE_FIXTURE !== '1') throw new Error('Unowned SDK permission mode fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(process.env, input.env ?? {})
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.setIsInteractive(false)
bootstrap.setAllowedSettingSources(['userSettings'])
const config = await import('../../src/utils/config')
config.enableConfigs()
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(input.settings ?? {}))
const setup = await import('../../src/utils/permissions/permissionSetup')
const auto = await import('../../src/utils/permissions/autoModeState')
const modes = await import('../../src/utils/permissions/PermissionMode')
auto._resetForTesting()
const initial = await setup.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], permissionMode: 'default', allowDangerouslySkipPermissions: !!input.allowBypass, addDirs: [] })
let context = initial.toolPermissionContext
const gate = await setup.verifyAutoModeGateAccess(context, false)
context = gate.updateContext(context)
const source = fs.readFileSync(new URL('../../src/cli/print.ts', import.meta.url), 'utf8')
const begin = source.indexOf('function handleSetPermissionMode(')
const end = source.indexOf('function handleChannelEnable(', begin)
if (begin < 0 || end < 0) throw new Error('Original permission control handler missing')
const featureValue = (name: string) => { if (name === 'TRANSCRIPT_CLASSIFIER') { if (feature('TRANSCRIPT_CLASSIFIER')) return true; return false }; throw new Error('Unbound Source build feature: ' + name) }
const dependencies = { feature: featureValue, isBypassPermissionsModeDisabled: setup.isBypassPermissionsModeDisabled, isAutoModeGateEnabled: setup.isAutoModeGateEnabled, getAutoModeUnavailableReason: setup.getAutoModeUnavailableReason, getAutoModeUnavailableNotification: setup.getAutoModeUnavailableNotification, transitionPermissionMode: setup.transitionPermissionMode }
const handler = new Function(...Object.keys(dependencies), new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(begin, end) + '\nreturn handleSetPermissionMode'))(...Object.values(dependencies))
const changeSource = fs.readFileSync(new URL('../../src/state/onChangeAppState.ts', import.meta.url), 'utf8')
const changeBegin = changeSource.indexOf('  const prevMode = oldState.toolPermissionContext.mode')
const changeEnd = changeSource.indexOf('  // mainLoopModel:', changeBegin)
if (changeBegin < 0 || changeEnd < 0) throw new Error('Original central mode observer missing')
const results = []
let ultraplan = false
for (const request of input.controls) {
  const frames: any[] = [], metadata: any[] = []
  const prior = context, priorUltraplan = ultraplan
  context = handler(request, 'mode-' + results.length, prior, { enqueue: (value: any) => frames.push(value) })
  ultraplan = request.ultraplan ?? ultraplan
  const changed = new Function('oldState', 'newState', 'toExternalPermissionMode', 'notifySessionMetadataChanged', 'notifyPermissionModeChanged', new Bun.Transpiler({ loader: 'ts' }).transformSync(changeSource.slice(changeBegin, changeEnd)))
  changed({ toolPermissionContext: prior, isUltraplanMode: priorUltraplan }, { toolPermissionContext: context, isUltraplanMode: ultraplan }, modes.toExternalPermissionMode, (value: any) => metadata.push(value), (mode: string) => frames.push({ type: 'system', subtype: 'status', status: null, permissionMode: mode }))
  results.push({ frames, metadata, mode: context.mode, prePlanMode: context.prePlanMode, autoActive: auto.isAutoModeActive() })
}
if (capabilities.blocked.length) throw new Error(`Source SDK mode capability escape: ${capabilities.blocked.join(', ')}`)
process.stdout.write(JSON.stringify({ results, capabilities }))
