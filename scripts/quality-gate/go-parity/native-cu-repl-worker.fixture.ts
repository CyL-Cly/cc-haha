import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Computer Use JavaScript worker Source comparison')
if (process.env.CC_HAHA_CU_WORKER_FIXTURE !== '1') throw new Error('Missing worker fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { createComputerUseReplWorker } = await import('../../../src/utils/computerUse/replWorker')
const { REPL_BOOTSTRAP_SOURCE } = await import('../../../src/vendor/computer-use-mcp/replApi')
const results = []
for (const row of input.cases) {
  const observations: unknown[] = []
  let active: unknown[] = []
  let completed: (() => void) | undefined
  const worker = createComputerUseReplWorker(message => {
    if (message.type === 'ready') return
    active.push(message)
    if (message.type === 'done') completed?.()
    if (message.type === 'invoke') queueMicrotask(() => {
      void worker.receive({ type: 'response', cellId: message.cellId, requestId: message.requestId,
        result: message.name === 'click' && 'x' in message.args && row.nativeResults
          ? { content: [] }
          : row.nativeResults?.[message.name] ?? row.nativeResult ?? { content: [{ type: 'text', text: 'native result' }] } })
    })
  })
  await worker.receive({ type: 'init', bootstrap: REPL_BOOTSTRAP_SOURCE })
  for (let index = 0; index < row.cells.length; index++) {
    active = []
    const done = new Promise<void>(resolve => { completed = resolve })
    await worker.receive({ type: 'run', cellId: index + 1, code: row.cells[index] })
    await done
    observations.push(active)
  }
  results.push(observations)
}
process.stdout.write(JSON.stringify(results))
