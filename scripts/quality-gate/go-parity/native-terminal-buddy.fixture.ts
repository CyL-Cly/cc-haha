import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual buddy appearance, legacy config and artwork')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BUDDY_SOURCE !== '1') throw new Error('Pinned buddy Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-buddy-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const companion = await import('../../../src/buddy/companion')
const sprites = await import('../../../src/buddy/sprites')
const results = []
for (const row of input.rows) {
  let roll, stored
  if (row.global) {
    config.saveGlobalConfig(current => { const next = { ...current }; delete next.companion; delete next.oauthAccount; delete next.userID; return { ...next, ...row.global } })
    roll = companion.roll(companion.companionUserId())
    stored = companion.getCompanion()
  } else roll = row.salted ? companion.roll(row.seed) : companion.rollWithSeed(row.seed)
  const value = stored ?? roll.bones
  results.push({ roll, stored: stored ?? null, face: sprites.renderFace(value), frames: [0, 1, 2, 3, 17].map(frame => sprites.renderSprite(value, frame)), count: sprites.spriteFrameCount(value.species) })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Buddy Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
