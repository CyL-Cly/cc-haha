import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original whole branch command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_BRANCH_SOURCE !== '1') throw new Error('Missing actual branch guard')
const caps = await installClientFixtureCapabilities(home)
process.chdir(home)
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(true)
state.switchSession(input.sessionId, null)
const { getTranscriptPath } = await import('../../../src/utils/sessionStorage')
const path = getTranscriptPath()
await mkdir(dirname(path), { recursive: true })
await writeFile(path, input.parent, { mode: 0o600 })
const { call } = await import('../../../src/commands/branch/branch')
const callbacks: any[] = []
let resumed: any
let entered: () => void, release: () => void
const resumeEntered = new Promise<void>(resolve => { entered = resolve })
const heldResume = new Promise<void>(resolve => { release = resolve })
const called = call((...args: any[]) => callbacks.push(args), {
  resume: async (sessionId: string, log: any, entrypoint: string) => {
    resumed = { sessionId, log, entrypoint }
    entered()
    if (input.holdResume) await heldResume
  },
} as any, input.argument)
let beforeResumeRelease: any
if (input.holdResume) {
  await resumeEntered
  const physicalFork = await readFile(resumed.log.fullPath, 'utf8')
  beforeResumeRelease = { callbacks: callbacks.length, forkCreated: physicalFork.includes('"forkedFrom"') }
  release()
}
await called
const parentAfter = await readFile(path, 'utf8')
const fork = resumed ? await readFile(resumed.log.fullPath, 'utf8') : null
if (caps.blocked.length) throw new Error('Whole branch unowned capability ' + caps.blocked.join(','))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ resumed, callbacks, parentUnchanged: parentAfter === input.parent, fork, caps, beforeResumeRelease }), error => error ? reject(error) : resolve()))
