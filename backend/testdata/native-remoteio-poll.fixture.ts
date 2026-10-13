import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const home = assertDisposableFixtureHome(process.env.HOME, 'Original bridge keepalive configuration')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned bridge poll fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(process.env, input.env)
fs.mkdirSync(join(home, '.claude'), { recursive: true })
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings'])
const config = await import('../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, ...input.global }))
const { getPollIntervalConfig } = await import('../../src/bridge/pollConfig')
const resolved = getPollIntervalConfig()
const timer = resolved.session_keepalive_interval_v2_ms > 0 ? setInterval(() => {}, resolved.session_keepalive_interval_v2_ms) : null
const effectiveInterval = timer ? (timer as any)._idleTimeout : 0
if (timer) clearInterval(timer)
if (capabilities.blocked.length) throw new Error('Original bridge poll capability escape: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ interval: resolved.session_keepalive_interval_v2_ms, effectiveInterval, capabilities }))
