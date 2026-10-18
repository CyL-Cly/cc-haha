import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Original terminal idle queue')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_IDLE_QUEUE_SOURCE !== '1') throw new Error('Missing Source idle queue guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const path = 'useQueueProcessor.ts'
const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../src/hooks/' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const declaration = source.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'useQueueProcessor')
if (!declaration) throw new Error('Original hook identity changed')
const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(declaration.getText(source)).replace('export ', '')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const queue = await import('../../../src/utils/messageQueueManager')
const { processQueueIfReady } = await import('../../../src/utils/queueProcessor')
const dependencies = {
  useSyncExternalStore: (_subscribe: unknown, getSnapshot: () => unknown) => getSnapshot(),
  useEffect: (effect: () => void) => effect(),
  getCommandQueueSnapshot: queue.getCommandQueueSnapshot,
  subscribeToCommandQueue: queue.subscribeToCommandQueue,
  processQueueIfReady,
}
const hook = new Function(...Object.keys(dependencies), body + '\nreturn useQueueProcessor')(...Object.values(dependencies))
const rows = []
for (const row of input.rows) {
  queue.clearCommandQueue()
  queue.enqueue({ value: '<task-notification>actual child result</task-notification>', mode: 'task-notification', priority: 'later' })
  let batches = 0
  hook({ executeQueuedInput: async () => { batches++ }, hasActiveLocalJsxUI: row.localUI, queryGuard: { subscribe: () => () => {}, getSnapshot: () => row.queryActive } })
  rows.push({ processed: batches === 1, remaining: queue.getCommandQueueLength() })
}
if (capabilities.blocked.length) throw new Error('Blocked actual idle queue: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, blocked: capabilities.blocked }))
process.exit(0)
