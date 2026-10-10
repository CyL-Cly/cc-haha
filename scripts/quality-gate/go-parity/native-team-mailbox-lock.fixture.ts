import { createInterface } from 'node:readline'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Team mailbox lease interoperability')
if (process.env.CC_HAHA_TEAM_MAILBOX_LOCK_FIXTURE !== '1') throw new Error('Missing team mailbox lock fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
Bun.plugin({ name: 'actual-mailbox-lock-options', setup(build) {
  build.onLoad({ filter: /\/teammateMailbox\.ts$/ }, async args => ({ contents: `${await Bun.file(args.path).text()}\nexport { LOCK_OPTIONS }`, loader: 'ts' }))
} })
const { LOCK_OPTIONS } = await import('../../../src/utils/teammateMailbox') as any
const { lock } = await import('../../../src/utils/lockfile')
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]()
const row = JSON.parse((await lines.next()).value!)
if (!row.target.startsWith(home + '/')) throw new Error('Target must be private')
const release = await lock(row.target, { lockfilePath: row.target + '.lock', ...LOCK_OPTIONS })
process.stdout.write(JSON.stringify({ ready: true, options: LOCK_OPTIONS }) + '\n')
await lines.next()
await release()
process.stdout.write(JSON.stringify({ released: true, capabilities }) + '\n')
process.exit(0)
