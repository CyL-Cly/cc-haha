import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'Native IM common state')
if (process.env.CC_HAHA_IM_COMMON_STATE_FIXTURE !== '1') throw new Error('Unowned IM common fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { isAllowedUser, tryPair } = await import('../../../adapters/common/pairing')
const { SessionStore } = await import('../../../adapters/common/session-store')
let now = input.now
Date.now = () => now
const config = process.env.CLAUDE_CONFIG_DIR!
mkdirSync(config, { recursive: true })
const file = join(config, 'adapters.json')
const pairing = []
for (const action of input.actions) {
  if (action.seed !== undefined) writeFileSync(file, JSON.stringify(action.seed))
  if (action.now !== undefined) now = action.now
  if (action.check !== undefined) pairing.push({ allowed: isAllowedUser('telegram', action.check) })
  if (action.text !== undefined) pairing.push({ paired: tryPair(action.text, { userId: action.user, displayName: action.name ?? 'Fixture' }, 'telegram'), config: JSON.parse(readFileSync(file, 'utf8')) })
}
const store = new SessionStore()
const bindings = []
for (const action of input.sessions) {
  if (action.seed !== undefined) writeFileSync(join(config, 'adapter-sessions.json'), JSON.stringify(action.seed))
  if (action.now !== undefined) now = action.now
  if (action.set) store.set(action.chat, action.id, action.work)
  if (action.remove) store.delete(action.chat)
  const removed = action.deleteSession ? store.deleteBySessionId(action.deleteSession) : undefined
  bindings.push({ entry: store.get(action.chat), all: store.listAll(), removed, stored: JSON.parse(readFileSync(join(config, 'adapter-sessions.json'), 'utf8')) })
}
process.stdout.write(JSON.stringify({ pairing, bindings }))
