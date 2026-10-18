import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original MCP command merge')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_COMMAND_MERGE !== '1') throw new Error('Pinned private Source required')
await import('yoga-layout')
const capabilities = await installClientFixtureCapabilities(home)
await import('../../../preload')
const React = await import('react')
const { render } = await import('../../../src/ink')
const { useMergedCommands } = await import('../../../src/hooks/useMergedCommands')
// Functions represent the mounted command objects: changing an unrelated MCP
// array must neither recreate them nor replace their captured old body.
const local = input.local.map((row: any) => ({ ...row, getPromptForCommand: () => row.body }))
const batches = input.mcp.map((rows: any[]) => rows.map(row => ({ ...row, getPromptForCommand: () => row.body })))
const observations: any[] = []
let index = 0
function Probe() {
  const merged = useMergedCommands(local, batches[index])
  observations[index] = {
    rows: merged.map(command => ({ name: command.name, body: (command as any).getPromptForCommand() })),
    sameLocalObjects: local.every((command: any) => merged.find(row => row.name === command.name) === command),
    emptyReturnsOriginalArray: batches[index].length === 0 ? merged === local : null
  }
  return null
}
const stdin = new PassThrough() as any
stdin.isTTY = true
stdin.setRawMode = () => {}
stdin.ref = () => stdin
stdin.unref = () => stdin
const stdout = new PassThrough() as any
stdout.columns = 120
stdout.isTTY = false
stdout.on('data', () => {})
const app = await render(React.createElement(Probe), { stdin, stdout, stderr: stdout, exitOnCtrlC: false, patchConsole: false })
try {
  for (index = 0; index < batches.length; index++) {
    app.rerender(React.createElement(Probe, { key: index }))
    const deadline = Date.now() + 5000
    while (!observations[index]) {
      if (Date.now() >= deadline) throw new Error('Actual Source hook did not render')
      await new Promise(resolve => setImmediate(resolve))
    }
  }
} finally {
  app.unmount()
}
if (capabilities.blocked.length) throw new Error('Unowned native capability: ' + capabilities.blocked.join(', '))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observations, blocked: capabilities.blocked }), () => resolve()))
process.exit(0)
