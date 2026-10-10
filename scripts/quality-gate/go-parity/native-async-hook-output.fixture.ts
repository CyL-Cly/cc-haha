import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { realpath } from 'node:fs/promises'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native asynchronous Hook output')
if (process.env.CC_HAHA_ASYNC_HOOK_FIXTURE !== '1') throw new Error('Unowned asynchronous Hook fixture')
await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TaskOutput } = await import('../../../src/utils/task/TaskOutput')
const { _resetTaskOutputDirForTest } = await import('../../../src/utils/task/diskOutput')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  _resetTaskOutputDirForTest()
  const output = new TaskOutput('async-source-' + index, null)
  for (const chunk of input.rows[index]) {
    if (chunk.spill) output.spillToDisk()
    else if (chunk.stderr) output.writeStderr(chunk.text)
    else output.writeStdout(chunk.text)
  }
  await output.flush()
  const stdout = await output.getStdout(), stderr = output.getStderr()
  const path = output.path
  if (!path.startsWith(home + '/') && !path.startsWith(await realpath(home) + '/')) throw new Error('Async output escaped private fixture root: ' + path)
  const disk = await Bun.file(path).exists() ? await Bun.file(path).text() : null
  output.clear()
  rows.push({ stdout, stderr, path, disk })
}
process.stdout.write(JSON.stringify({ rows }))
