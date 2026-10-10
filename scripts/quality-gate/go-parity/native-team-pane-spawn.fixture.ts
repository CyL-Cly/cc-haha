import { chmod, mkdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import * as children from 'node:child_process'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalSpawn = children.spawn, originalBunSpawn = Bun.spawn.bind(Bun)
assertDisposableFixtureHome(input.homeDir, 'Actual pane split producer')
if (process.env.CC_HAHA_TEAM_PANE_FIXTURE !== '1') throw new Error('Missing pane fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
const directory = join(input.homeDir, 'pane-bin'), log = join(input.homeDir, 'pane-args')
await mkdir(directory, { recursive: true })
for (const command of ['tmux', 'it2']) { const path = join(directory, command); await Bun.write(path, input.binaryScript); await chmod(path, 0o755) }
process.env.PATH = `${directory}:/usr/bin:/bin`
process.env.CLAUDE_CODE_TMPDIR = join(input.homeDir, 'private-temp'); await mkdir(process.env.CLAUDE_CODE_TMPDIR, { recursive: true })
process.env.CC_HAHA_PANE_LOG = log; process.env.CC_HAHA_PANE_COUNT = join(input.homeDir, 'pane-count')
if (input.inside) { process.env.TMUX = 'private-launch'; process.env.TMUX_PANE = '%0' } else delete process.env.TMUX
if (input.backend === 'iterm2') process.env.ITERM_SESSION_ID = 'w0t0p0:original-leader-session'
else delete process.env.ITERM_SESSION_ID
const permitted = (file: string, args: string[], options: any) => {
 if (!['tmux', 'it2', join(directory, 'tmux'), join(directory, 'it2')].includes(file) || options?.shell || options?.cwd && options.cwd !== input.homeDir) throw new Error('Pane subprocess escaped private scripts')
 if (!input.producer && (args.includes('send-keys') && !args.includes(input.command) || args.includes('run') && !args.includes(input.command))) throw new Error('Unexpected pane command')
}
const spawn = ((file: string, args: string[], options: any) => { permitted(file, args, options); return originalSpawn(file, args, options) }) as typeof children.spawn
require('node:child_process').spawn = spawn; spyOn(children, 'spawn').mockImplementation(spawn)
Bun.spawn = ((command: any, nativeOptions: any = {}) => { const options = Array.isArray(command) ? nativeOptions : command, args = Array.isArray(command) ? command : command.cmd; permitted(args[0], args.slice(1), options); return originalBunSpawn(command, nativeOptions) }) as typeof Bun.spawn
syncBuiltinESMExports()
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir); bootstrap.setOriginalCwd(input.homeDir)
Bun.plugin({ name: 'actual-pane-full-producer-export', setup(build) { build.onLoad({ filter: /\/spawnMultiAgent\.ts$/ }, async args => ({ contents: `${await Bun.file(args.path).text()}\nexport { handleSpawnSplitPane, buildInheritedCliFlags }`, loader: 'ts' })) } })
const { TmuxBackend } = await import('../../../src/utils/swarm/backends/TmuxBackend')
const { ITermBackend } = await import('../../../src/utils/swarm/backends/ITermBackend')
let producer: any
if (input.producer) {
 const { enableConfigs } = await import('../../../src/utils/config'); enableConfigs()
 const { setCliTeammateModeOverride, captureTeammateModeSnapshot } = await import('../../../src/utils/swarm/backends/teammateModeSnapshot'); setCliTeammateModeOverride('tmux'); captureTeammateModeSnapshot()
 const { writeTeamFileAsync, readTeamFileAsync } = await import('../../../src/utils/swarm/teamHelpers')
 const { readMailbox } = await import('../../../src/utils/teammateMailbox')
 const { clearTeammateColors, assignTeammateColor } = await import('../../../src/utils/swarm/teammateLayoutManager')
 bootstrap.switchSession(input.producer.sessionId); bootstrap.setIsInteractive(true); bootstrap.setSessionBypassPermissionsMode(input.producer.mode === 'bypassPermissions'); bootstrap.setMainLoopModelOverride(input.producer.leaderModel);
 process.env.CLAUDE_CODE_TEAMMATE_COMMAND = input.producer.binary
 if (input.producer.now !== undefined) spyOn(Date, 'now').mockReturnValue(input.producer.now)
 clearTeammateColors()
 if (input.producer.teamContext?.leadAgentId) assignTeammateColor(input.producer.teamContext.leadAgentId)
 await writeTeamFileAsync(input.producer.team.name, input.producer.team)
 let state: any = { mainLoopModel: input.producer.leaderModel, effortValue: input.producer.effort, toolPermissionContext: { mode: input.producer.mode }, tasks: {}, teamContext: input.producer.teamContext }
 const source: any = await import('../../../src/tools/shared/spawnMultiAgent')
 const result = await source.handleSpawnSplitPane(input.producer.input, { toolUseId: input.producer.toolUseId, options: { agentDefinitions: { activeAgents: input.producer.definitions ?? [] }, thinkingConfig: input.producer.thinkingConfig }, getAppState: () => state, setAppState: (update: any) => { state = update(state) } })
 const task: any = Object.values(state.tasks)[0]; delete task.abortController
 producer = { result, task, context: state.teamContext, team: await readTeamFileAsync(input.producer.team.name), mailbox: await readMailbox(result.data.name,input.producer.team.name) }
}
const backend = input.backend === 'tmux' ? new TmuxBackend() : new ITermBackend(), rows = []
for (const name of input.names ?? []) {
 const pane = await backend.createTeammatePaneInSwarmView(name, 'purple')
 if (pane.isFirstTeammate && input.inside) await backend.enablePaneBorderStatus()
 await backend.sendCommandToPane(pane.paneId, input.command, !input.inside)
 rows.push(pane)
}
const args = (await readFile(log, 'utf8')).trim().split('\n').map(record => JSON.parse(record).map((value: string) => /^claude-swarm-\d+$/.test(value) ? 'claude-swarm-PID' : value))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, args, producer, capabilities }), () => resolve()))
process.exit(0)
