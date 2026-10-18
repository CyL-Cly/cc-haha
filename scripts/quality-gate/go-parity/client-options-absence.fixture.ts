import fs from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Exact absent path primitive')
if (process.env.CC_HAHA_ABSENCE_FIXTURE !== '1') throw new Error('Exact absent fixture guard required')
const caps = await installClientFixtureCapabilities(home, { absentPaths: [input.absent] })
const observations: any[] = []
function read(name: string, value: any) {
  try { fs.statSync(value); observations.push({ name, success: true }) }
  catch (error: any) { observations.push({ name, success: false, code: error.code ?? null, message: error.message }) }
}
read('declared-string', input.absent)
read('declared-buffer', Buffer.from(input.absent))
read('declared-file-url', pathToFileURL(input.absent))
read('undeclared-string', input.sentinel)
read('undeclared-buffer', Buffer.from(input.sentinel))
read('undeclared-file-url', pathToFileURL(input.sentinel))
read('non-file-url', new URL('https://fixture.invalid/absent'))
read('escape', join(home, '..', 'outside-sentinel'))
read('symlink', join(home, 'outside-link'))
process.stdout.write(JSON.stringify({ observations, absentReads: caps.absentReads, blocked: caps.blocked }))
