import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process AppState callbacks')
if (process.env.CC_HAHA_INPROCESS_TEAM_STATE_FIXTURE !== '1') throw new Error('Missing actual teammate state fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const { appendCappedMessage } = await import('../../../src/tasks/InProcessTeammateTask/types')
const { parse } = await import('acorn')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/swarm/inProcessRunner.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const arrows: any[] = []
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'ArrowFunctionExpression') arrows.push(node)
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') walk(value)
  }
}
walk(ast)
const observe = arrows.find(node => {
  const body = source.slice(node.start, node.end)
  return node.params[0]?.name === 'task' && body.includes('let inProgressToolUseIDs = task.inProgressToolUseIDs') && body.includes('appendCappedMessage')
})
const terminal = arrows.filter(node => node.params[0]?.name === 'task' && source.slice(node.start, node.end).includes('alreadyTerminal = true'))
if (!observe || terminal.length !== 2) throw new Error('Original teammate state callbacks changed')
const update = new Function('appendCappedMessage', 'message', 'progress', `return (${source.slice(observe.start, observe.end)})`)
const finish = terminal.map(node => new Function('Date', 'errorMessage', `let alreadyTerminal = false; let toolUseId; const run = (${source.slice(node.start, node.end)}); return task => ({ task: run(task), changed: !alreadyTerminal })`))
const normalize = (task: any) => {
  const result = { ...task }
  if (result.inProgressToolUseIDs !== undefined) result.inProgressToolUseIDs = [...result.inProgressToolUseIDs]
  return result
}
let task: any = input.task
if (task.inProgressToolUseIDs !== undefined) task.inProgressToolUseIDs = new Set(task.inProgressToolUseIDs)
const observations = input.messages.map((message: any, index: number) => {
  task = update(appendCappedMessage, message, input.progress[index])(task)
  return normalize(task)
})
const finishes = input.finishes.map((row: any) => {
  const result = finish[row.status === 'failed' ? 1 : 0]({ now: () => row.now }, row.failure)(row.task)
  return { ...result, task: normalize(result.task) }
})
process.stdout.write(JSON.stringify({ observations, finishes, capabilities }))
