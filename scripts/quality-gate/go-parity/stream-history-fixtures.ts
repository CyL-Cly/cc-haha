export type StreamHistoryFixtureInput = {
  homeDir: string
  wire: string
  historyPrefixSources?: string[]
  model?: string
  nonInteractive?: boolean
  // The HTTP fixture grants only this explicit loopback origin. The ordinary
  // history fixture continues to return its deterministic handed Response.
  httpOrigin?: string
}

export type StreamHistoryObservation = {
  profile: {
    tools: []
    thinking: 'disabled'
    querySource: 'sdk'
    nonStreamingFallback: 'disabled'
    nodeEnv: 'production'
    maxOutputTokens: 32000
    model: string
    nonInteractive: boolean
  }
  wire: string
  historyPrefixBytes: string[]
  startMessageBytes: string | null
  startUsageBytes: string | null
  events: Array<{ envelopeBytes: string; eventBytes: string }>
  assistants: Array<{
    uuid: string
    envelopeBytesAtYield: string
    messageBytesAtYield: string
    envelopeBytesAfterStream: string
    messageBytesAfterStream: string
    usageSharesStartAtYield: boolean
    usageSharesStartAfterStream: boolean
    usageAliasGroup: number
    isApiError: boolean
  }>
  timeline: Array<Record<string, unknown>>
  uuidCalls: number
  timeCalls: number
  normalized: Array<{
    envelopeBytes: string
    messageBytes: string
    sourceAssistantIndex: number | null
    usageAliasesAssistantIndexes: number[]
    contentAliases: Array<{ assistantIndex: number; blockIndex: number } | null>
  }>
  historyBytesAfterNormalization: string[]
  error: { name: string; message: string } | null
  normalizationError: { name: string; message: string } | null
  fetchCount: number
}

export type StreamHistoryScenario = { name: string; wire: string; model?: string; nonInteractive?: boolean; expectsStartEvent?: boolean }

function event(type: string, fields: Record<string, unknown> = {}) {
  return `event: ${type}\ndata: ${JSON.stringify({ type, ...fields })}\n\n`
}

function start(message?: Record<string, unknown>) {
  return event('message_start', { message: message ?? {
    future: 'provider-first',
    usage: { input_tokens: 7, output_tokens: 0, extension: 'start-only' },
    id: 'provider-id', stop_reason: null, content: [], role: 'assistant', type: 'message', model: 'fixture-model',
  } })
}

function blockStart(index: number, content_block: Record<string, unknown>) {
  return event('content_block_start', { index, content_block })
}

function delta(index: number, value: Record<string, unknown>) {
  return event('content_block_delta', { index, delta: value })
}

function stop(index: number) { return event('content_block_stop', { index }) }

function text(index: number, value: string, complete = true) {
  return blockStart(index, { future: `text-${index}`, text: 'ignored initial text', type: 'text' })
    + delta(index, { type: 'text_delta', text: value }) + (complete ? stop(index) : '')
}

function tool(index: number, id: string, type = 'tool_use', partial = '{"z":1,"a":2}', complete = true) {
  return blockStart(index, { input: { ignored: true }, future: `tool-${index}`, caller: { type: 'direct' }, name: 'FixtureTool', id, type })
    + delta(index, { type: 'input_json_delta', partial_json: partial }) + (complete ? stop(index) : '')
}

function usage(deltaValue: Record<string, unknown>, counts: Record<string, unknown>) {
  return event('message_delta', { delta: deltaValue, usage: counts })
}

function terminal(reason: unknown = 'end_turn', messageStop = true) {
  return usage({ stop_reason: reason, stop_sequence: null }, { output_tokens: 8 })
    + (messageStop ? event('message_stop') : '')
}

// These inputs exercise the actual API generator's envelope and reference
// behavior. No flattened protocol result is used to construct expected output.
export const streamHistoryScenarios: StreamHistoryScenario[] = [
  { name: 'reverse-block-stops-and-repeat', wire: start()
    + text(0, 'first index', false) + text(1, 'second index', false)
    + stop(1) + stop(0) + stop(0) + terminal() },
  { name: 'ordinary-completed-text', wire: start() + text(0, '中文🙂 first') + text(1, 'second') + terminal() },
  { name: 'usage-between-block-stops', wire: start() + text(0, 'first')
    + usage({ stop_reason: null }, { output_tokens: 3, input_tokens: 0 })
    + text(1, 'second') + terminal()
    + usage({ stop_reason: null }, { output_tokens: 11, cache_read_input_tokens: 9 }) },
  { name: 'local-tool-defers-and-parses-input', wire: start() + text(0, 'before local')
    + tool(1, 'local-call', 'tool_use', '{"z":1,"a":2,"large":9007199254740993,"high":"\\ud800","low":"\\udfff"}')
    + text(2, 'after local') + terminal('tool_use') },
  { name: 'thinking-tool-text-and-signature-replacement', wire: start()
    + blockStart(0, { future: { z: 1, a: 2 }, thinking: 'ignored', type: 'thinking' })
    + delta(0, { type: 'thinking_delta', thinking: 'private thought' })
    + delta(0, { type: 'signature_delta', signature: 'old signature' })
    + delta(0, { type: 'signature_delta', signature: 'new signature' }) + stop(0)
    + tool(1, 'thinking-call') + text(2, 'public answer') + terminal('tool_use') },
  { name: 'server-tool-early-yield-mutates-last-envelope', wire: start() + text(0, 'before server')
    + tool(1, 'server-call', 'server_tool_use') + terminal() },
  { name: 'server-tool-early-yield-with-later-text', wire: start() + text(0, 'before server')
    + tool(1, 'server-call', 'server_tool_use')
    + usage({ stop_reason: null }, { output_tokens: 4 }) + text(2, 'after server') + terminal() },
  { name: 'tool-id-dedup-across-local-and-server', wire: start() + tool(0, 'same-id')
    + tool(1, 'same-id', 'server_tool_use') + stop(1) + tool(2, 'other-id') + terminal('tool_use') },
  { name: 'partial-eof-keeps-completed-order-then-index-order', wire: start()
    + text(2, 'unfinished two', false) + text(1, 'completed one') + text(0, 'unfinished zero', false) },
  { name: 'eof-discards-completed-local-tool', wire: start() + text(0, 'visible') + tool(1, 'discarded-tool') },
  { name: 'max-token-limit-discards-tool-after-allocation', wire: start() + text(0, 'before limit')
    + tool(1, 'limited-tool') + text(2, 'after tool') + terminal('max_tokens', false) },
  { name: 'context-limit-text-and-incomplete-block', wire: start() + text(0, 'completed')
    + text(1, 'not preserved at limit', false) + terminal('model_context_window_exceeded', false) },
  { name: 'unknown-block-pass-through-and-utf16-order', wire: start({
    '\ud800': 'high-key', '\udfff': 'low-key', future: { z: 1, a: 2 }, id: 'provider-id',
    content: [], usage: { extension: 'first', input_tokens: 7, output_tokens: 0 },
    type: 'message', model: 'fixture-model', role: 'assistant',
  }) + blockStart(0, { future: { z: 1, a: 2 }, '\ud800': 'high', '\udfff': 'low', type: 'mcp_tool_result', content: [{ text: 'opaque', type: 'text' }], id: 'opaque-id' })
    + stop(0) + blockStart(1, { future: true, '\ud800': 'key', type: 'text' })
    + delta(1, { type: 'text_delta', text: '\ud800x\udfff' }) + stop(1) + terminal() },
  { name: 'post-server-invalid-delta-produces-source-api-error', wire: start()
    + tool(0, 'server-call', 'server_tool_use') + text(1, 'visible before error')
    + delta(5, { type: 'text_delta', text: 'missing block' }) },
  { name: 'malformed-tool-json-marker-survives-history', wire: start()
    + tool(0, 'malformed-call', 'tool_use', '\ud800{') + text(1, 'continued') + terminal('tool_use') },
  { name: 'empty-terminal-has-no-assistant-envelope', wire: start() + terminal() },
  { name: 'start-omits-usage-and-stop-fields', wire: start({ id: 'provider-id', type: 'message', role: 'assistant', model: 'fixture-model', content: [] }) + text(0, 'without initial usage') + terminal() },
  { name: 'thinking-only-is-orphaned-history', wire: start() + blockStart(0, { type: 'thinking' })
    + delta(0, { type: 'thinking_delta', thinking: 'private' }) + delta(0, { type: 'signature_delta', signature: 'signed' }) + stop(0) + terminal() },
  { name: 'whitespace-and-trailing-thinking-history', wire: start() + text(0, '\ufeff\t\n')
    + blockStart(1, { type: 'redacted_thinking', data: 'opaque' }) + stop(1) + terminal() },
  { name: 'local-tools-with-interleaved-text-reordered', wire: start() + tool(0, 'first-tool') + text(1, 'between tools') + tool(2, 'second-tool') + text(3, 'after tools') + terminal('tool_use') },
  { name: 'max-token-text-feedback', wire: start() + text(0, 'limited text') + terminal('max_tokens', false) },
  { name: 'context-limit-discards-local-tool', wire: start() + tool(0, 'limited-tool') + text(1, 'context text') + terminal('model_context_window_exceeded', false) },
  { name: 'usage-delta-omits-usage', wire: start() + text(0, 'before absent usage')
    + event('message_delta', { delta: { stop_reason: null } }) + text(1, 'last') + terminal() },
  { name: 'signature-omitted-then-restored', wire: start() + blockStart(0, { signature: 'ignored', type: 'thinking' })
    + delta(0, { type: 'thinking_delta', thinking: 'private' }) + delta(0, { type: 'signature_delta' })
    + delta(0, { type: 'signature_delta', signature: 'restored' }) + stop(0) + text(1, 'answer') + terminal() },
  { name: 'missing-block-stop-feedback', wire: start() + tool(0, 'server-call', 'server_tool_use') + stop(5) },
  { name: 'post-server-delta-type-mismatch-feedback', wire: start() + tool(0, 'server-call', 'server_tool_use')
    + text(1, 'incomplete', false) + delta(1, { type: 'thinking_delta', thinking: 'mismatch' }) },
  { name: 'repeated-limit-updates-already-yielded-text', wire: start() + text(0, 'limited text') + terminal('max_tokens', false)
    + usage({ stop_reason: 'max_tokens', stop_sequence: 'future' }, { output_tokens: 11 }) },
  { name: 'typed-text-coercion-keeps-utf16', wire: start() + blockStart(0, { type: 'text' })
    + [null, true, ['\ud800', null, 2], { future: true }, undefined].map(text => delta(0, { type: 'text_delta', text })).join('') + stop(0) + terminal() },
  { name: 'text-mutates-after-stop-and-early-yield', wire: start() + text(0, 'first snapshot') + tool(1, 'server-call', 'server_tool_use')
    + delta(0, { type: 'text_delta', text: ' changed after yield' }) + terminal() },
  { name: 'tool-id-utf16-distinct-and-replay', wire: start()
    + ['\ud800', '\udfff', '\ufffd', '\ud800', '😀', '\ud83d\ude00'].map((id, index) => tool(index, id)).join('') + terminal('tool_use') },
  { name: 'empty-stream-source-feedback', wire: '' },
  { name: 'block-stop-before-message-start-feedback', wire: tool(0, 'server-call', 'server_tool_use') },
  { name: 'repeated-index-overwrites-block-but-suppresses-second-stop', wire: start()
    + tool(0, 'first-index-tool') + tool(0, 'replacement-index-tool', 'server_tool_use')
    + text(1, 'after replacement') + terminal('tool_use') },
  { name: 'signature-on-text-produces-source-feedback', wire: start() + text(0, 'visible')
    + delta(0, { type: 'signature_delta', signature: 'mismatch' }) },
  { name: 'input-json-on-text-produces-source-feedback', wire: start() + text(0, 'visible')
    + delta(0, { type: 'input_json_delta', partial_json: '{}' }) },
  { name: 'tool-terminal-with-no-block-produces-source-feedback', wire: start() + terminal('tool_use') },
  { name: 'unknown-delta-type-remains-raw-event', wire: start() + blockStart(0, { type: 'text' })
    + delta(0, { type: 'future_delta', data: { z: 1, a: 2 } })
    + delta(0, { type: 'text_delta', text: 'known text' }) + stop(0) + terminal() },
  { name: 'pending-message-stop-at-eof-is-not-dispatched', wire: start() + text(0, 'visible')
    + terminal('end_turn', false) + event('message_stop').trimEnd() + '\r\n' },
  { name: 'pending-terminal-delta-at-eof-is-not-dispatched', wire: start() + text(0, 'visible')
    + usage({ stop_reason: 'end_turn' }, { output_tokens: 99 }).trimEnd() },
  ...[true, false].flatMap(nonInteractive => ['fixture-model', 'claude-sonnet-4-20250514'].map(model => ({
    name: `refusal-${nonInteractive ? 'sdk' : 'interactive'}-${model === 'fixture-model' ? 'other-model' : 'suggested-model'}`,
    nonInteractive, model, wire: start() + text(0, 'provider refusal') + terminal('refusal'),
  }))),
  { name: 'refusal-before-tool-drain-and-repeat', wire: start() + text(0, 'before refusal')
    + tool(1, 'pending-tool') + terminal('refusal', false)
    + usage({ stop_reason: 'refusal' }, { output_tokens: 11 }) + event('message_stop') },
  { name: 'refusal-without-provider-blocks', wire: start() + terminal('refusal') },
  { name: 'refusal-before-provider-eof-feedback', wire: start() + text(0, 'before refusal')
    + tool(1, 'pending-tool') + terminal('refusal', false) },
  { name: 'usage-coercion-retains-original-values-and-cumulative-aliases', wire: start() + tool(0, 'server-usage', 'server_tool_use')
    + ['12', true, false, '', '\ufeff +3.5e1 \n', '\u0085 9', 'Infinity', '+Infinity', '-Infinity', 'inf', '+Inf', '0x10', '0X000', '0b10', '0o10', '-0x1', '+0b1', '1_0', '12tail', ['9'], [[2]], [], [null], [1, 2], {}, { valueOf: 8 }, ['\ud800'], 'NaN', '0x', '0b2', '0o8', '0x1g', '0XaF', '.5', '5.', '-0', '1e-400', '1e400', '+ 1', null, undefined]
      .map((value, index) => usage({ stop_reason: null }, { input_tokens: value, cache_creation_input_tokens: value, cache_read_input_tokens: value, output_tokens: index }) + text(index + 1, `usage projection ${index}`)).join('') + terminal() },
  { name: 'usage-coercion-numeric-overflow-stays-internal-until-serialization', wire: start() + tool(0, 'server-usage', 'server_tool_use')
    + ['1e400', '1e-400', '9007199254740993', '-1e400'].map((value, index) =>
      usage({ stop_reason: null }, { input_tokens: '__raw__', cache_creation_input_tokens: 0, output_tokens: index }).replace('"__raw__"', value) + text(index + 1, `raw projection ${index}`)).join('') + terminal() },
  { name: 'usage-coercion-falsy-and-truthy-nonobject-usage', wire: start() + tool(0, 'server-usage', 'server_tool_use')
    + [false, 0, '', null, undefined, [], 'future', 4].map((value, index) => event('message_delta', { delta: { stop_reason: null }, usage: value }) + text(index + 1, `usage shape ${index}`)).join('') + terminal() },
  { name: 'usage-coercion-start-object-own-tostring-source-feedback', expectsStartEvent: false,
    wire: start({ id: 'provider-id', type: 'message', role: 'assistant', model: 'fixture-model', content: [], usage: { input_tokens: { toString: null } } }) + text(0, 'unreachable') + terminal() },
  { name: 'usage-coercion-start-array-object-own-tostring-source-feedback', expectsStartEvent: false,
    wire: start({ id: 'provider-id', type: 'message', role: 'assistant', model: 'fixture-model', content: [], usage: { input_tokens: [{ toString: 'not callable' }] } }) + text(0, 'unreachable') + terminal() },
  { name: 'usage-coercion-delta-throws-before-last-envelope-mutation', wire: start() + text(0, 'before server')
    + tool(1, 'server-usage', 'server_tool_use') + usage({ stop_reason: 'end_turn' }, { input_tokens: '11', cache_creation_input_tokens: { toString: false }, output_tokens: 99 }) + event('message_stop') },
  ...[null, undefined].flatMap(partial_json => ['tool_use', 'server_tool_use'].map(firstToolType => ({
    name: `tool-fragment-${partial_json === null ? 'null' : 'missing'}-after-${firstToolType}`,
    wire: start() + text(0, 'completed text') + tool(1, 'completed-tool', firstToolType)
      + blockStart(2, { type: 'tool_use', id: 'incomplete-tool', name: 'fixture_tool' })
      + delta(2, { type: 'input_json_delta', partial_json }) + stop(2) + terminal('tool_use'),
  }))),
]
