import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import type { AssistantMessage, Message } from '../../../src/types/message'
import type { StreamHistoryFixtureInput, StreamHistoryObservation } from './stream-history-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as StreamHistoryFixtureInput
assertDisposableFixtureHome(input.homeDir, 'Stream history')
if (process.env.CC_HAHA_STREAM_HISTORY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK !== '1') {
  throw new Error('Stream history fixture requires explicit disposable HOME/config')
}
// The host snapshot has no system managed policy and no external credentials.
// Only exact managed-file reads receive observable ENOENT; parsing/merging,
// auth/client decisions and the query generator remain real Source code.
if ((input.httpOrigin && process.env.CC_HAHA_STREAM_HTTP_FIXTURE !== '1') || (process.env.CC_HAHA_STREAM_HTTP_FIXTURE === '1' && !input.httpOrigin)) throw new Error('Stream history HTTP requires its explicit fixture profile and origin')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.httpOrigin })
// Guard first, then load the real API graph. No production query, parser,
// normalization, commit buffer, or SDK method is replaced with a fixture.
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
const model = input.model ?? 'fixture-model'
const nonInteractive = input.nonInteractive ?? true
bootstrap.setIsInteractive(!nonInteractive)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { createUserMessage, normalizeMessagesForAPI } = await import('../../../src/utils/messages')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')

const history: Message[] = input.historyPrefixSources?.map(source => JSON.parse(source)) ?? [createUserMessage({
  content: 'Stream history fixture', uuid: 'fixture-user', timestamp: '2023-11-14T22:13:20.000Z',
})]
const result: StreamHistoryObservation = {
  // NODE_ENV=test invokes the production VCR's whole-stream buffering and
  // hides true yield-time mutation and allocation order. Keep it disabled
  // through the real production profile, not by mocking the wrapper.
  profile: { tools: [], thinking: 'disabled', querySource: 'sdk', nonStreamingFallback: 'disabled', nodeEnv: 'production', maxOutputTokens: 32000, model, nonInteractive },
  wire: input.wire, historyPrefixBytes: history.map(message => JSON.stringify(message)),
  startMessageBytes: null, startUsageBytes: null, events: [], assistants: [], timeline: [], uuidCalls: 0, timeCalls: 0,
  normalized: [], historyBytesAfterNormalization: [], error: null, normalizationError: null, fetchCount: 0,
}
let phase = 'query'
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = `fixture-id-${++result.uuidCalls}`
  result.timeline.push({ kind: 'uuid', phase, value })
  return value as ReturnType<typeof crypto.randomUUID>
})
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) {
    super(...(args.length ? args : [1_700_000_000_000]) as [number])
    if (!args.length) {
      result.timeCalls++
      result.timeline.push({ kind: 'time', phase, value: this.toISOString() })
    }
  }
  static now() { return 1_700_000_000_000 }
} as DateConstructor

const assistants: AssistantMessage[] = []
let startUsage: unknown
const controller = new AbortController()
function errorRecord(failure: unknown) {
  if (!(failure instanceof Error)) throw failure
  // Stack frames are runtime metadata; the original error name and message
  // are exact, and are not rewritten into a made-up compatibility error.
  return { name: failure.name, message: failure.message }
}
try {
  for await (const part of queryModelWithStreaming({
    messages: history, systemPrompt: asSystemPrompt([]), thinkingConfig: { type: 'disabled' }, tools: [], signal: controller.signal,
    options: {
      model, querySource: 'sdk', isNonInteractiveSession: nonInteractive, maxOutputTokensOverride: 32_000,
      agents: [], mcpTools: [], hasAppendSystemPrompt: false,
      getToolPermissionContext: async () => getEmptyToolPermissionContext(),
      fetchOverride: async (url, init) => {
        result.fetchCount++
        const actual = new URL(String(url))
        if (result.fetchCount !== 1 || actual.origin !== (input.httpOrigin ?? 'http://127.0.0.1:43210') || actual.pathname !== '/v1/messages' || init?.method !== 'POST') {
          throw new Error('Unexpected stream history fixture provider request')
        }
        if (input.httpOrigin) return fetch(String(url), init)
        const bytes = new TextEncoder().encode(input.wire)
        let offset = 0
        return new Response(new ReadableStream<Uint8Array>({
          pull(streamController) {
            if (offset === bytes.length) return streamController.close()
            streamController.enqueue(bytes.slice(offset, offset + 1))
            offset++
          },
        }), { headers: { 'content-type': 'text/event-stream', 'request-id': 'fixed-request' } })
      },
    },
  })) {
    if (part.type === 'stream_event') {
      const event = part.event
      if (event.type === 'message_start') {
        startUsage = event.message.usage
        result.startMessageBytes = JSON.stringify(event.message)
        result.startUsageBytes = JSON.stringify(startUsage) ?? null
      }
      result.events.push({ envelopeBytes: JSON.stringify(part), eventBytes: JSON.stringify(event) })
      result.timeline.push({ kind: 'stream_event', phase, eventIndex: result.events.length - 1, eventType: event.type, ...('index' in event ? { blockIndex: event.index } : {}) })
    } else if (part.type === 'assistant') {
      assistants.push(part)
      result.assistants.push({
        uuid: part.uuid, envelopeBytesAtYield: JSON.stringify(part), messageBytesAtYield: JSON.stringify(part.message),
        envelopeBytesAfterStream: '', messageBytesAfterStream: '', usageSharesStartAtYield: part.message.usage === startUsage,
        usageSharesStartAfterStream: false, usageAliasGroup: -1, isApiError: part.isApiErrorMessage === true,
      })
      result.timeline.push({ kind: 'assistant', phase, assistantIndex: assistants.length - 1, uuid: part.uuid })
    } else result.timeline.push({ kind: 'other_message', phase, envelopeBytes: JSON.stringify(part) })
  }
} catch (failure) { result.error = errorRecord(failure) }
finally { controller.abort() }

const usageObjects: unknown[] = [startUsage]
for (const [index, assistant] of assistants.entries()) {
  const observation = result.assistants[index]!
  observation.envelopeBytesAfterStream = JSON.stringify(assistant)
  observation.messageBytesAfterStream = JSON.stringify(assistant.message)
  observation.usageSharesStartAfterStream = assistant.message.usage === startUsage
  if (!usageObjects.includes(assistant.message.usage)) usageObjects.push(assistant.message.usage)
  observation.usageAliasGroup = usageObjects.indexOf(assistant.message.usage)
}
phase = 'normalize'
try {
  const normalized = normalizeMessagesForAPI([...history, ...assistants], [], 'fixture-model')
  result.normalized = normalized.map(message => ({
    envelopeBytes: JSON.stringify(message), messageBytes: JSON.stringify(message.message),
    sourceAssistantIndex: message.type === 'assistant' ? assistants.findIndex(assistant => assistant.uuid === message.uuid) : null,
    usageAliasesAssistantIndexes: message.type === 'assistant' ? assistants.flatMap((assistant, index) => assistant.message.usage === message.message.usage ? [index] : []) : [],
    contentAliases: Array.isArray(message.message.content) ? message.message.content.map(block => {
      for (const [assistantIndex, assistant] of assistants.entries()) {
        const blockIndex = assistant.message.content.indexOf(block as typeof assistant.message.content[number])
        if (blockIndex !== -1) return { assistantIndex, blockIndex }
      }
      return null
    }) : [],
  }))
} catch (failure) { result.normalizationError = errorRecord(failure) }
result.historyBytesAfterNormalization = [...history, ...assistants].map(message => JSON.stringify(message))
// Credential-store observations belong only to the Source host boundary;
// they do not become fabricated Go auth calls or cross-language observations.
process.stdout.write(`${JSON.stringify({ observationSource: JSON.stringify(result), capabilities })}\n`)
