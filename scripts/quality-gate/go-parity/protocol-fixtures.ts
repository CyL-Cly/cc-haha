import { openaiChatStreamToAnthropic } from '../../../src/server/proxy/streaming/openaiChatStreamToAnthropic'
import { openaiResponsesStreamToAnthropic } from '../../../src/server/proxy/streaming/openaiResponsesStreamToAnthropic'

export const protocolFixtureNames = [
  'chat-eof', 'chat-length', 'chat-error', 'chat-completed',
  'responses-incomplete', 'responses-failed', 'responses-done-only',
  'anthropic-duplicate', 'anthropic-eof', 'anthropic-truncated',
  'chat-malformed', 'chat-corrected', 'chat-mixed',
  'text-partial-eof', 'empty-end-turn', 'thinking-text',
  'usage-only-null-stop', 'utf8-crlf-multiline', 'context-limit-text',
  'typed-text-coercion', 'typed-thinking-signature', 'typed-tool-fragment',
  'typed-terminal-number', 'empty-terminal-reason', 'typed-signature-omitted',
  'tool-number-precision', 'tool-lone-surrogate', 'malformed-lone-surrogate',
  'typed-text-infinity', 'tool-id-utf16', 'content-order-utf16',
  'stop-eof', 'stop-single-lf', 'stop-single-cr', 'stop-single-crlf',
  'stop-blank-cr', 'stop-blank-crlf', 'delta-eof', 'delta-single-lf', 'delta-blank-lf',
  'utf8-invalid-continuation', 'utf8-truncated-prefix', 'utf8-out-of-range', 'utf8-valid-prefix-truncated',
  'bom-each-line', 'bom-data-line',
] as const

export type ProtocolFixtureName = typeof protocolFixtureNames[number]
export type ProtocolFixture = { wire: string; wireBytes?: never } | { wire?: never; wireBytes: number[] }

function event(type: string, fields: Record<string, unknown>): string {
  return `event: ${type}\ndata: ${JSON.stringify({ type, ...fields })}\n\n`
}

function upstream(wire: string): ReadableStream<Uint8Array> {
  const bytes = new TextEncoder().encode(wire)
  let offset = 0
  return new ReadableStream({
    pull(controller) {
      if (offset === bytes.length) return controller.close()
      controller.enqueue(bytes.slice(offset, offset + 1))
      offset++
    },
  })
}

const args = JSON.stringify({ file_path: '/fixture/中文🙂.txt', content: 'written exactly once' })
const malformedArgs = '{"file_path":"/fixture/invalid.txt","content": invalid}'

function chatTool(calls: Array<{ id: string; arguments: string }>, finish?: string) {
  return `data: ${JSON.stringify({ choices: [{ index: 0, delta: { tool_calls: calls.map((call, index) => ({ index, id: call.id, type: 'function', function: { name: 'FixtureWrite', arguments: call.arguments } })) }, finish_reason: finish ?? null }] })}\n\n`
}

function start() {
  return event('message_start', { message: { id: 'msg_fixture', type: 'message', role: 'assistant', model: 'fixture-model', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 10, output_tokens: 0 } } })
}

function block(index: number, value: Record<string, unknown>, delta: Record<string, unknown>, stop = true) {
  return event('content_block_start', { index, content_block: value }) + event('content_block_delta', { index, delta }) + (stop ? event('content_block_stop', { index }) : '')
}

function terminal(reason: unknown = 'end_turn', stop = true) {
  return event('message_delta', { delta: { stop_reason: reason, stop_sequence: null }, usage: { output_tokens: 5 } }) + (stop ? event('message_stop', {}) : '')
}

/** These are provider response boundaries, not a second agent-loop simulator.
 * OpenAI fixtures pass through the existing TS proxy before both runtimes read
 * the same Anthropic wire. This proves the Go codec's commit behavior only.
 */
export async function protocolFixture(name: ProtocolFixtureName): Promise<ProtocolFixture> {
  if (name.startsWith('chat-')) {
    const calls = name === 'chat-malformed'
      ? [{ id: 'call_fixture', arguments: malformedArgs }]
      : name === 'chat-corrected'
        ? [{ id: 'call_corrected', arguments: args }]
        : name === 'chat-mixed'
          ? [{ id: 'call_valid', arguments: args }, { id: 'call_fixture', arguments: malformedArgs }]
          : [{ id: 'call_fixture', arguments: args }]
    let wire = chatTool(calls, name === 'chat-length' ? 'length' : name === 'chat-eof' || name === 'chat-error' ? undefined : 'tool_calls')
    if (name === 'chat-error') wire += `data: ${JSON.stringify({ error: { type: 'server_error', message: 'fixture upstream failure' } })}\n\n`
    return { wire: await new Response(openaiChatStreamToAnthropic(upstream(wire), 'fixture-model')).text() }
  }
  if (name.startsWith('responses-')) {
    const status = name === 'responses-failed' ? 'failed' : name === 'responses-incomplete' ? 'incomplete' : 'completed'
    const wire = event('response.created', { response: { id: 'resp_fixture', model: 'fixture-model' } })
      + event('response.output_item.added', { output_index: 0, item: { id: 'fc_fixture', type: 'function_call', call_id: 'call_fixture', name: 'FixtureWrite', arguments: '' } })
      + (name === 'responses-done-only' ? '' : event('response.function_call_arguments.delta', { item_id: 'fc_fixture', delta: args }))
      + event('response.function_call_arguments.done', { item_id: 'fc_fixture', arguments: args })
      + event(`response.${status}`, { response: { status, ...(status === 'failed' ? { error: { type: 'server_error', message: 'fixture upstream failure' } } : {}), ...(status === 'incomplete' ? { incomplete_details: { reason: 'max_output_tokens' } } : {}), usage: { input_tokens: 10, output_tokens: 5 } } })
    return { wire: await new Response(openaiResponsesStreamToAnthropic(upstream(wire), 'fixture-model')).text() }
  }
  let wire = start()
  if (name.startsWith('stop-') || name.startsWith('delta-')) {
    const endings: Record<string, string> = { eof: '', 'single-lf': '\n', 'single-cr': '\r', 'single-crlf': '\r\n', 'blank-cr': '\r\r', 'blank-crlf': '\r\n\r\n', 'blank-lf': '\n\n' }
    wire += block(0, { type: 'tool_use', id: 'eof-call', name: 'FixtureWrite', input: {} }, { type: 'input_json_delta', partial_json: args })
    const ending = endings[name.slice(name.indexOf('-') + 1)]!
    if (name.startsWith('stop-')) {
      wire += terminal('tool_use', false)
      return { wire: wire + event('message_stop', {}).trimEnd() + ending }
    }
    return { wire: wire + terminal('tool_use', false).trimEnd() + ending }
  }
  if (name.startsWith('utf8-invalid-') || name === 'utf8-truncated-prefix' || name === 'utf8-out-of-range' || name === 'utf8-valid-prefix-truncated') {
    const corrupt = name === 'utf8-invalid-continuation' ? [0x80] : name === 'utf8-truncated-prefix' ? [0xe2, 0x82] : name === 'utf8-out-of-range' ? [0xf4, 0x90, 0x80, 0x80] : [0xf4, 0x8f, 0x80]
    wire += event('content_block_start', { index: 0, content_block: { type: 'text', text: '' } })
    const before = new TextEncoder().encode(wire + 'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"')
    const after = new TextEncoder().encode('"}}\n\n' + event('content_block_stop', { index: 0 }) + terminal())
    return { wireBytes: [...before, ...corrupt, ...after] }
  }
  if (name === 'bom-each-line' || name === 'bom-data-line') {
    wire += block(0, { type: 'text', text: '' }, { type: 'text_delta', text: '\ufeffinside text' }) + terminal()
    return { wire: wire.split('\n').map(line => line && (name === 'bom-each-line' || line.startsWith('data:')) ? '\ufeff' + line : line).join('\n') }
  }
  if (name === 'content-order-utf16') {
    wire += event('content_block_start', { index: 0, content_block: { future: { z: 1, a: 2 }, '\ud800': 'high', '\udfff': 'low', '\ufffd': 'replacement', text: 'ignored', type: 'text' } })
    for (const text of ['\ud800', 'x\udfff', '\ud83d', '\ude00', ['\ud800', null, '\udfff']]) {
      wire += event('content_block_delta', { index: 0, delta: { type: 'text_delta', text } })
    }
    wire += event('content_block_stop', { index: 0 })
    wire += event('content_block_start', { index: 1, content_block: { signature: 'ignored', future: true, thinking: 'ignored', type: 'thinking' } })
    wire += event('content_block_delta', { index: 1, delta: { type: 'thinking_delta', thinking: '\ud800\udfff' } })
    wire += event('content_block_delta', { index: 1, delta: { type: 'signature_delta' } })
    wire += event('content_block_delta', { index: 1, delta: { type: 'signature_delta', signature: '\ud800' } })
    wire += event('content_block_stop', { index: 1 })
    wire += block(2, { input: {}, caller: { kind: 'fixture' }, name: 'FixtureWrite', id: 'ordered-call', type: 'tool_use' }, { type: 'input_json_delta', partial_json: '{"z":1,"a":2}' })
    return { wire: wire + terminal('tool_use') }
  }
  if (name === 'tool-id-utf16') {
    // JS Set compares UTF-16 code units. A transport codec must distinguish
    // both lone surrogates from U+FFFD and still suppress actual ID replays.
    for (const [index, id] of ['\ud800', '\udfff', '\ufffd', '\ud800', '😀', '\ud83d\ude00'].entries()) {
      wire += block(index, { type: 'tool_use', id, name: 'FixtureWrite', input: {} }, { type: 'input_json_delta', partial_json: args })
    }
    return { wire: wire + terminal('tool_use') }
  }
  if (name === 'typed-text-infinity') {
    wire += event('content_block_start', { index: 0, content_block: { type: 'text', text: '' } })
    // Keep literal overflow numbers on the wire: constructing a JS object
    // first would stringify Infinity as null and hide the provider boundary.
    for (const value of ['1e400', '-1e400', '[1e400,null,-1e400]', '1e-400']) {
      wire += `event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":${value}}}\n\n`
    }
    return { wire: wire + event('content_block_stop', { index: 0 }) + terminal() }
  }
  if (name === 'tool-number-precision' || name === 'tool-lone-surrogate' || name === 'malformed-lone-surrogate') {
    const partial = name === 'tool-number-precision'
      ? '{"integer":9007199254740993,"large":1e400,"negativeLarge":-1e400,"tiny":1e-400,"scientific":1e21,"negativeZero":-0,"nested":[9007199254740993]}'
      : name === 'tool-lone-surrogate'
        ? '{"high":"\\ud800","low":"\\udfff","pair":"\\ud83d\\ude00"}'
        : '\ud800{'
    wire += block(0, { type: 'tool_use', id: 'json-call', name: 'FixtureWrite', input: {} }, { type: 'input_json_delta', partial_json: partial })
    return { wire: wire + terminal('tool_use') }
  }
  if (name === 'typed-text-coercion') {
    wire += event('content_block_start', { index: 0, content_block: { type: 'text', text: '' } })
    for (const value of [null, 3, -0, 1e20, 1e21, 1e-6, 1e-7, true, { proxy: true }, ['array', null, 4], undefined]) {
      wire += event('content_block_delta', { index: 0, delta: { type: 'text_delta', text: value } })
    }
    return { wire: wire + event('content_block_stop', { index: 0 }) + terminal() }
  }
  if (name === 'typed-thinking-signature') {
    wire += block(0, { type: 'thinking', thinking: '', signature: '' }, { type: 'thinking_delta', thinking: null }, false)
    wire += event('content_block_delta', { index: 0, delta: { type: 'signature_delta', signature: null } })
    return { wire: wire + event('content_block_stop', { index: 0 }) + terminal() }
  }
  if (name === 'typed-tool-fragment') {
    wire += block(0, { type: 'tool_use', id: 'typed-call', name: 'FixtureWrite', input: {} }, { type: 'input_json_delta', partial_json: 3 })
    return { wire: wire + terminal('tool_use') }
  }
  if (name === 'typed-terminal-number' || name === 'empty-terminal-reason') {
    wire += block(0, { type: 'text', text: '' }, { type: 'text_delta', text: 'typed terminal' })
    return { wire: wire + terminal(name === 'typed-terminal-number' ? 3 : '') }
  }
  if (name === 'typed-signature-omitted') {
    wire += block(0, { type: 'thinking', thinking: '', signature: '' }, { type: 'thinking_delta', thinking: 'private thought' }, false)
    wire += event('content_block_delta', { index: 0, delta: { type: 'signature_delta' } })
    return { wire: wire + event('content_block_stop', { index: 0 }) + terminal() }
  }
  if (name.startsWith('anthropic-')) {
    for (const index of name === 'anthropic-duplicate' ? [0, 1] : [0]) {
      wire += block(index, { type: 'tool_use', id: 'call_fixture', name: 'FixtureWrite', input: {} }, { type: 'input_json_delta', partial_json: args }) + event('content_block_stop', { index })
    }
    return { wire: wire + terminal(name === 'anthropic-truncated' ? 'max_tokens' : 'tool_use', name === 'anthropic-duplicate') }
  }
  if (name === 'empty-end-turn') return { wire: wire + terminal() }
  if (name === 'thinking-text') {
    wire += block(0, { type: 'thinking', thinking: '', signature: '' }, { type: 'thinking_delta', thinking: 'private thought' }, false)
    wire += event('content_block_delta', { index: 0, delta: { type: 'signature_delta', signature: 'signed_thinking' } }) + event('content_block_stop', { index: 0 })
    wire += block(1, { type: 'text', text: '' }, { type: 'text_delta', text: 'answer 中文🙂' })
  } else {
    wire += block(0, { type: 'text', text: 'ignored duplicate initial text' }, { type: 'text_delta', text: 'answer 中文🙂' }, name !== 'text-partial-eof')
  }
  if (name === 'text-partial-eof') return { wire }
  if (name === 'context-limit-text') return { wire: wire + terminal('model_context_window_exceeded', false) }
  wire += terminal()
  if (name === 'usage-only-null-stop') {
    wire += event('message_delta', { delta: { stop_reason: null, stop_sequence: null }, usage: { input_tokens: 0, output_tokens: 9 } })
  }
  if (name === 'utf8-crlf-multiline') {
    wire = wire.replaceAll('\n', '\r\n').replace('data: {"type":"message_start",', 'data: {\r\ndata: "type":"message_start",')
  }
  return { wire }
}
