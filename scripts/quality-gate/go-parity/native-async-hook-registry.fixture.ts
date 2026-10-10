import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native asynchronous hooks')
if (process.env.CC_HAHA_ASYNC_HOOK_FIXTURE !== '1') throw new Error('Unowned asynchronous Hook fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const original = await Bun.file(new URL('../../../src/utils/hooks/AsyncHookRegistry.ts', import.meta.url)).text()
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(original)
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const code = ast.body.filter((node: any) => node.type !== 'ImportDeclaration').map((node: any) => {
  const body = node.type === 'ExportNamedDeclaration' ? node.declaration : node
  return source.slice(body.start, body.end)
}).join('\n')
const create = new Function('logForDebugging', 'invalidateSessionEnvCache', 'jsonParse', 'jsonStringify', 'emitHookResponse', 'startHookProgressInterval', 'Date', code + '\nreturn { registerPendingAsyncHook, checkForAsyncHookResponses, finalizePendingAsyncHooks, getPendingAsyncHooks }')
const rows = []
for (const profile of input.rows) {
  let invalidations = 0
  const events: any[] = [], cleanup: string[] = [], stopped: string[] = [], killed: string[] = []
  const registry = create(() => {}, () => invalidations++, JSON.parse, JSON.stringify, (value: any) => events.push(value), (value: any) => () => stopped.push(value.hookId), { now: () => 123 })
  for (const row of profile.hooks) {
    const shell = {
      status: row.status,
      taskOutput: { getStdout: async () => row.stdout, getStderr: () => row.stderr },
      result: Promise.resolve({ code: row.code }),
      cleanup: () => cleanup.push(row.id),
      kill: () => { killed.push(row.id); shell.status = 'killed' },
    }
    registry.registerPendingAsyncHook({ processId: row.id, hookId: 'hook-' + row.id, asyncResponse: { async: true, asyncTimeout: row.timeout }, hookName: row.name ?? 'PostToolUse:Write', hookEvent: row.event ?? 'PostToolUse', command: 'private-command', shellCommand: shell, toolName: row.toolName, pluginId: row.pluginId })
  }
  const before = registry.getPendingAsyncHooks().map((row: any) => ({ processId: row.processId, timeout: row.timeout, startTime: row.startTime }))
  const responses = await registry.checkForAsyncHookResponses()
  const pending = registry.getPendingAsyncHooks().map((row: any) => row.processId)
  if (profile.finalize) await registry.finalizePendingAsyncHooks()
  rows.push({ before, responses, pending, events, cleanup, stopped, killed, invalidations })
}
process.stdout.write(JSON.stringify({ rows }))
