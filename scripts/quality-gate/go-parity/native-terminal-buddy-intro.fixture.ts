import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual companion intro producer, original attachment renderer and observer')
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
const actual = await import('../../../src/buddy/prompt')
const messages = await import('../../../src/utils/messages')
const observer = await import('../../../src/buddy/observer')
const results = []
for (const row of input.rows) {
  config.saveGlobalConfig(current => { const next = { ...current }; delete next.companion; delete next.companionMuted; return { ...next, ...row.global } })
  const before = JSON.stringify(row.history)
  const attachments = actual.getCompanionIntroAttachment(row.history)
  const rendered = attachments.flatMap(item => messages.normalizeAttachmentForAPI(item)).map(item => ({ type: item.type, content: item.message.content, isMeta: item.isMeta }))
  const reactions: string[] = []
  const originalRandom = Math.random
  Math.random = () => 0
  await observer.fireCompanionObserver(row.history, text => reactions.push(text))
  Math.random = originalRandom
  results.push({ attachments, rendered, reactions, before, after: JSON.stringify(row.history) })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Buddy intro Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
