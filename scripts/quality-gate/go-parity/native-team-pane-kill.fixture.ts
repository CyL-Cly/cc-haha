import { chmod, mkdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import * as children from 'node:child_process'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalSpawn = children.spawn, originalBunSpawn = Bun.spawn.bind(Bun)
assertDisposableFixtureHome(input.homeDir, 'Actual pane shutdown subprocess')
if (process.env.CC_HAHA_TEAM_PANE_FIXTURE !== '1') throw new Error('Missing pane fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
const directory = join(input.homeDir, 'pane-bin'), log = join(input.homeDir, 'pane-args')
await mkdir(directory, { recursive: true })
for (const command of ['tmux', 'it2']) {
  const path = join(directory, command)
  await Bun.write(path, '#!/bin/sh\nprintf "%s\\0" "$@" >> "$CC_HAHA_PANE_LOG"\nprintf "\\0" >> "$CC_HAHA_PANE_LOG"\nexit "$CC_HAHA_PANE_EXIT"\n')
  await chmod(path, 0o755)
}
process.env.PATH = `${directory}:/usr/bin:/bin`
process.env.CC_HAHA_PANE_LOG = log
const permitted = (file: string, args: string[], options: any) => {
  if (options?.shell || options?.cwd && options.cwd !== input.homeDir) throw new Error('Pane subprocess escaped private fixture')
  const allowed = input.rows.some((row: any) => {
    const expected = row.backend === 'tmux' ? [...(row.external ? ['-L', `claude-swarm-${process.pid}`] : []), 'kill-pane', '-t', row.pane] : ['session', 'close', '-f', '-s', row.pane]
    return [row.backend === 'tmux' ? 'tmux' : 'it2', join(directory, row.backend === 'tmux' ? 'tmux' : 'it2')].includes(file) && JSON.stringify(args) === JSON.stringify(expected)
  })
  if (!allowed) throw new Error('Undeclared pane subprocess')
}
const spawn = ((file: string, args: string[], options: any) => { permitted(file, args, options); return originalSpawn(file, args, options) }) as typeof children.spawn
require('node:child_process').spawn = spawn
spyOn(children, 'spawn').mockImplementation(spawn)
Bun.spawn = ((command: any, nativeOptions: any = {}) => { const options = Array.isArray(command) ? nativeOptions : command, args = Array.isArray(command) ? command : command.cmd; permitted(args[0], args.slice(1), options); return originalBunSpawn(command, nativeOptions) }) as typeof Bun.spawn
syncBuiltinESMExports()
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir); bootstrap.setOriginalCwd(input.homeDir)
const { TmuxBackend } = await import('../../../src/utils/swarm/backends/TmuxBackend')
const { ITermBackend } = await import('../../../src/utils/swarm/backends/ITermBackend')
const rows = []
for (const row of input.rows) {
  process.env.CC_HAHA_PANE_EXIT = String(row.code)
  const backend = row.backend === 'tmux' ? new TmuxBackend() : new ITermBackend()
  rows.push(await backend.killPane(row.pane, row.external))
}
const args = (await readFile(log, 'utf8')).split('\0\0').filter(Boolean).map(record => record.split('\0').map(value => /^claude-swarm-\d+$/.test(value) ? 'claude-swarm-PID' : value))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, args, capabilities }), () => resolve()))
process.exit(0)
