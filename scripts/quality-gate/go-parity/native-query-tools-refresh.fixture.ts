import { queryLoopScenarios } from './query-loop-fixtures'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Interactive query tool refresh')
if (process.env.CC_HAHA_QUERY_LOOP_FIXTURE !== '1') throw new Error('Missing actual query fixture marker')
const original = queryLoopScenarios.find(s => s.name === 'interactive-duplicate-end-turn-none')!
const payload = {
  ...original,
  home,
  origin: input.origin,
  runtime: { platform: process.platform, arch: process.arch, version: process.version },
  deviceID: 'fixture-query-device',
  sessionID: '00000000-0000-4000-8000-000000000000',
  refreshTools: 'replace-after-tools',
}
const child = Bun.spawn([process.execPath, '--no-env-file', join(import.meta.dir, 'query-loop.fixture.ts')], {
  cwd: home,
  env: process.env,
  stdin: new Blob([JSON.stringify(payload)]),
  stdout: 'pipe',
  stderr: 'pipe',
})
const [out, error, code] = await Promise.all([
  new Response(child.stdout).text(),
  new Response(child.stderr).text(),
  child.exited,
])
process.stdout.write(out)
process.stderr.write(error)
process.exitCode = code
