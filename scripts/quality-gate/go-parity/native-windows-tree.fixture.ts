import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Windows process tree')
if (process.env.CC_HAHA_NATIVE_WINDOWS_TREE_FIXTURE !== '1') throw new Error('Windows tree fixture guard missing')
const { resolveWindowsTaskkillExecutable, killWindowsProcessTree } = await import('../../../src/utils/windowsProcessTree')
const rows = []
for (const row of input.rows) {
  let command: string[] | null = null
  let options: unknown = null
  let killed = false
  const ok = await killWindowsProcessTree(row.pid, {
    env: row.env,
    timeoutMs: 3,
    spawn: (cmd, opts) => {
      command = cmd
      options = opts
      if (row.failure === 'start') throw new Error('fixture spawn failure')
      return {
        exited: row.failure === 'timeout' ? new Promise<number>(() => {}) : Promise.resolve(row.failure === 'exit' ? 1 : 0),
        kill: () => { killed = true },
      }
    },
  })
  rows.push({ executable: resolveWindowsTaskkillExecutable(row.env), ok, command, options, killed })
}
process.stdout.write(JSON.stringify(rows))
