import { assertDisposableFixtureHome } from './fixture-guard'
import { realpathSync } from 'node:fs'
import { isAbsolute, relative, sep } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native memory')
if (process.env.CC_HAHA_NATIVE_MEMORY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Native memory requires isolated production fixture')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['cwd', 'operations'].includes(key))) throw new Error('Undeclared memory fixture input')
const within = relative(realpathSync(home), realpathSync(input.cwd))
if (!isAbsolute(input.cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned memory project')
process.chdir(input.cwd)
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd)
state.setProjectRoot(input.cwd)
state.setCwdState(input.cwd)
const { handleMemoryApi } = await import('../../../src/server/api/memory')
const observations = []
for (const op of input.operations) {
  if (Object.keys(op).some(key => !['method', 'path', 'body'].includes(key))) throw new Error('Undeclared memory operation')
  const url = new URL(op.path, 'http://127.0.0.1')
  if (url.origin !== 'http://127.0.0.1' || !url.pathname.startsWith('/api/memory')) throw new Error('Undeclared memory endpoint')
  const response = await handleMemoryApi(new Request(url, { method: op.method, body: op.body }), url, url.pathname.split('/').filter(Boolean))
  observations.push({ status: response.status, body: await response.text() })
}
process.stdout.write(JSON.stringify(observations))
