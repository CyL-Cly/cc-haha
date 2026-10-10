import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Native task lock interoperability')
if (process.env.CC_HAHA_NATIVE_TASK_LOCK_FIXTURE !== '1') throw new Error('Task lock fixture requires isolated capability')
const capabilities = await installClientFixtureCapabilities(home)
const target = join(home, '.claude', 'tasks', 'interop', '.lock')
await mkdir(join(target, '..'), { recursive: true })
await writeFile(target, '')
const { lock } = await import('../../../src/utils/lockfile')
const release = await lock(target, { retries: { retries: 30, minTimeout: 5, maxTimeout: 100 } })
process.stdout.write('SOURCE_LOCKED\n')
for await (const chunk of Bun.stdin.stream()) { if (chunk.length) break }
await release()
process.stdout.write(JSON.stringify({ released: true, capabilities }) + '\n')
