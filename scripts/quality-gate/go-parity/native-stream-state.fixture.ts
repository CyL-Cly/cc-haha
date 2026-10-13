import { createStreamWatchdogState } from '../../../src/services/api/streamWatchdog'
import { StreamAssistantCommitBuffer } from '../../../src/services/api/streamAssistantCommitBuffer'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Native stream state')
if (process.env.CC_HAHA_NATIVE_STREAM_STATE_FIXTURE !== '1') throw new Error('Unowned stream-state fixture')
const state = createStreamWatchdogState()
const results = input.events.map((event: unknown) => {
  const first = state.recordEvent(event)
  const errors = ['idle', 'max_duration', 'tool_input_duration'].map((reason: any) => {
    const error = state.createTimeoutError(reason, 1500)
    return { message: error.message, safe: error.safeToRetryStream(), enumerable: JSON.parse(JSON.stringify(error)), diagnostic: JSON.stringify(error.toDiagnosticData()) }
  })
  return { first, snapshot: state.snapshot(), errors }
})
const buffer = new StreamAssistantCommitBuffer({ deferToolUseCommit: true })
const commits = input.blocks.map((block: string, index: number) => ({ yielded: buffer.add(index, block), crossed: buffer.hasCrossedSideEffectBoundary(), committed: buffer.hasCommitted() }))
process.stdout.write(JSON.stringify({ results, commits }))
