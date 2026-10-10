export type SDKStreamScenario = {
  name: string
  wireBytes: number[]
  chunks?: number[]
  headers?: Array<[string, string]>
  status?: number
  deepArray?: boolean
  deepErrorNumbers?: 'future'
}
export type SDKStreamObservation = {
  yields: Array<{ source: string; numberLeaves: Record<string, { value: string; negativeZero: boolean }> }>
  error: null | {
    kind: string; class: string; name: string; message: string
    status: { kind: string; source?: string }
    requestID: { kind: string; source?: string }
    payload: { kind: string; source?: string; numberLeaves?: Record<string, { value: string; negativeZero: boolean }> }
    headers: Array<[string, string]> | null
  }
  diagnostics: Array<{ message: string; valueSource: string }>
  headers: Array<[string, string]>
  requestID: string | null
}
const bytes = (wire: string) => [...new TextEncoder().encode(wire)]
const frame = (event: string, data?: string, end = '\n\n') => `event: ${event}\n${data === undefined ? '' : `data: ${data.replaceAll('\n', '\ndata: ')}`}${end}`
const scenario = (name: string, wire: string, rest: Omit<SDKStreamScenario, 'name' | 'wireBytes'> = {}): SDKStreamScenario => ({ name, wireBytes: bytes(wire), ...rest })
const deepSingletonArray = '['.repeat(10001) + '0' + ']'.repeat(10001)
export const sdkStreamSyntaxSources = [
  ['empty', ''], ['done', '[DONE]'], ['bareidentifier', 'not-json'], ['objectkey', '{bad:1}'],
  ['trailingcomma', '{"a":1,}'], ['arraycomma', '[1,]'], ['unterminatedstring', '"tail'],
  ['control', '"line\nfeed"'], ['badescape', '"\\x"'], ['badunicode', '"\\uZZZZ"'],
  ['badnumber', '01'], ['trailingtoken', '{"a":1}tail'], ['incompleteobject', '{"a":'],
  ['object-open', '{'], ['array-open', '['], ['object-close', '}'], ['array-close', ']'], ['colon', ':'], ['comma', ','],
  ['at', '@'], ['null-extra', 'nullx'], ['minus', '-'], ['minus-dot', '-.1'], ['dot', '.1'], ['decimal', '1.'],
  ['exponent', '1e'], ['exponent-sign', '1e+'], ['double-minus', '--1'], ['array-no-comma', '[1 2]'],
  ['missing-colon', '{"a" 1}'], ['missing-comma', '{"a":1 "b":2}'], ['double-comma-object', '{"a":1,,}'],
  ['commas-array', '[,,]'], ['double-comma-array', '[1,,2]'], ['two-values', 'true false'], ['comment', '/*hi*/ {}'],
  ['singlequote', "'abc'"], ['array-exponent', '[1e]'], ['object-exponent', '{"a":1e}'], ['missing-value', '{"a":}'],
  ['comma-in-colon-slot', '{"a",1}'], ['object-key-eof', '{"a"'], ['string-escape-eof', '"abc\\'],
  ['array-null-extra', '[nullx]'], ['object-leading-zero', '{"a":01}'], ['array-decimal', '[1.,2]'],
  ['object-at', '{ @}'], ['plus', '+1'], ['parenthesis', '(1)'], ['non-ascii-token', 'é'],
  ['short-unicode-escape', '"\\u12"'], ['emoji-token', '🙂'], ['control-nul', '"a\0b"'], ['control-tab', '"a\tb"'],
] as const
export const sdkStreamScenarios: SDKStreamScenario[] = [
  scenario('six-message-events-and-legacy', ['message_start', 'content_block_start', 'content_block_delta', 'content_block_stop', 'message_delta', 'message_stop', 'completion'].map((event, index) => frame(event, JSON.stringify({ type: event, index }))).join('')),
  scenario('event-name-dispatch-keeps-wrong-body-types', frame('message_start', '{"type":"error","error":{"message":"not an SSE error"}}') + frame('content_block_delta', '{"type":"message_stop","future":true}') + frame('completion', '{"type":"ping"}')),
  scenario('recognized-events-yield-all-json-value-types', ['null', '[]', '[1e400,-0,9007199254740993]', '1e400', '-0', 'false', '"\\ud800"'].map(data => frame('message_delta', data)).join('')),
  scenario('yield-object-numbers-order-duplicates-and-utf16', frame('content_block_delta', '{"2":2,"1":1,"z":1e400,"a":9007199254740993,"duplicate":1,"duplicate":-0,"\\ud800":"\\udfff"}')),
  scenario('valid-array-beyond-encoding-json-depth-limit', frame('completion', deepSingletonArray), { deepArray: true }),
  scenario('ping-unknown-empty-and-data-only-ignore-invalid-json', frame('ping', '{bad') + frame('future_event', '[DONE]') + frame('', 'not-json') + 'data: {bad\n\n' + frame('message_stop', 'true')),
  scenario('unknown-done-does-not-end-later-known-event', 'data: [DONE]\n\n' + frame('future_event', '[DONE]') + frame('completion', '{"after":true}')),
  scenario('event-only-recognized-parses-empty-data', frame('message_stop')),
  scenario('event-only-unknown-is-ignored', frame('future_event') + frame('ping') + frame('', undefined) + frame('message_stop', '{}')),
  scenario('event-field-without-colon-is-empty', 'event\ndata: invalid\n\n' + frame('completion', '42')),
  scenario('last-event-field-wins', 'event: error\nevent: message_start\ndata: {"type":"future"}\n\n'),
  scenario('multiline-data-comments-and-unknown-fields', ': comment\nevent: completion\nid: ignored\nretry: 3\ndata: {"a":\ndata: 1,"b":2}\n\n'),
  scenario('completion-invalid-json-stops-before-next-event', frame('completion', '{bad') + frame('message_stop', '{}')),
  ...sdkStreamSyntaxSources.map(([name, data]) => scenario(`syntax-${name}`, frame('message_delta', data))),
  ...[
    ['full-body', '{"type":"error","error":{"type":"overloaded_error","message":"nested message"},"future":1}'],
    ['top-string-message', '{"message":"top-level message","error":{"message":"nested"}}'],
    ['top-object-message', '{"message":{"2":2,"1":1,"future":"\\ud800"}}'],
    ['top-number-message', '{"message":1e400}'],
    ['top-falsy-message', '{"message":0,"future":true}'],
    ['null', 'null'], ['false', 'false'], ['zero', '0'], ['number', '1e400'], ['string', '"plain"'], ['array', '[false,1e400]'],
    ['invalid', '{bad'], ['empty', ''],
  ].map(([name, data]) => scenario(`sse-error-${name}`, frame('error', data!), { headers: [['request-id', 'request-fixture'], ['x-future', ' retained ']] })),
  ...[
    ['selected-shallow-message', `{"message":"selected","future":${deepSingletonArray}}`],
    ['message-after-deep-field', `{"future":${deepSingletonArray},"message":"selected"}`],
    ['duplicate-top-message', `{"message":"first","future":${deepSingletonArray},"message":"selected"}`],
    ['escaped-property-names', `{"messa\\u0067e":"escaped-selected","fu\\u0074ure":${deepSingletonArray}}`],
    ['duplicate-escaped-top-message', `{"message":"first","future":${deepSingletonArray},"messa\\u0067e":"escaped-selected"}`],
    ['escaped-first-top-message', `{"messa\\u0067e":"first","future":${deepSingletonArray},"message":"selected"}`],
    ['selected-object-message', `{"future":${deepSingletonArray},"message":{"z":1,"2":2,"1":1,"surrogate":"\\ud800"}}`],
    ['selected-overflow-message', `{"future":${deepSingletonArray},"message":1e400}`],
    ['falsy-message-keeps-full-payload', `{"future":${deepSingletonArray},"message":0}`],
    ['null-message-keeps-full-payload', `{"future":${deepSingletonArray},"message":null}`],
  ].map(([name, data]) => scenario(`sse-deep-error-${name}`, frame('error', data!), { deepErrorNumbers: 'future' })),
  scenario('http-deep-error-selected-shallow-message', `{"message":"selected","future":${deepSingletonArray}}`, { status: 400, deepErrorNumbers: 'future' }),
  scenario('http-deep-error-duplicate-escaped-top-message', `{"message":"first","future":${deepSingletonArray},"messa\\u0067e":"escaped-selected"}`, { status: 400, deepErrorNumbers: 'future' }),
  scenario('sse-error-request-id-absent', frame('error', '{"message":"missing id"}')),
  scenario('sse-error-empty-request-id-dropped-by-bun-fetch', frame('error', '{"message":"empty id"}'), { headers: [['request-id', '']] }),
  scenario('sse-error-mixed-case-request-id-last-nonempty', frame('error', '{"message":"duplicate id"}'), { headers: [['request-id', 'first'], ['Request-Id', 'second']] }),
  ...[
    ['same-request-id', [['request-id', 'first'], ['request-id', 'second']]],
    ['same-future', [['x-future', 'first'], ['x-future', 'second']]],
    ['mixed-future', [['x-future', 'first'], ['X-Future', 'second']]],
    ['empty-future', [['x-future', '']]],
    ['empty-after-future', [['x-future', 'first'], ['x-future', '']]],
    ['empty-before-future', [['x-future', ''], ['x-future', 'second']]],
    ['whitespace-future', [['x-future', '  \t ']]],
    ['same-content-type', [['content-type', 'first/type'], ['content-type', 'second/type']]],
    ['mixed-content-type', [['Content-Type', 'third/type']]],
    ['empty-content-type', [['content-type', '']]],
    ['same-set-cookie', [['set-cookie', 'first=a'], ['set-cookie', 'second=b']]],
    ['mixed-set-cookie', [['set-cookie', 'first=a'], ['Set-Cookie', 'second=b']]],
    ['empty-set-cookie', [['set-cookie', '']]],
    ['combined-request-id', [['request-id', 'first, second']]],
  ].map(([name, headers]) => scenario(`response-headers-${name}`, frame('message_stop', '{}'), { headers: headers as Array<[string, string]> })),
  ...[
    ['cache-control', 'private', 'no-store'], ['server', 'fixture-first', 'fixture-second'],
    ['date', 'Fri, 02 Jan 1970 00:00:00 GMT', 'Sat, 03 Jan 1970 00:00:00 GMT'],
    ['retry-after', '1', '2'], ['www-authenticate', 'Basic fixture-first', 'Bearer fixture-second'],
    ['location', '/fixture-first', '/fixture-second'], ['x-request-id', 'first', 'second'],
    ['x-anthropic-future', 'first', 'second'], ['anthropic-ratelimit-requests-limit', '11', '22'],
    ['anthropic-ratelimit-tokens-limit', '11', '22'], ['content-encoding', 'identity', 'identity'],
    ['access-control-allow-origin', 'https://first.invalid', 'https://second.invalid'],
  ].flatMap(([name, first, second]) => [
    scenario(`response-probe-${name}-duplicate`, frame('message_stop', '{}'), { headers: [[name!, first!], [name!.replace(/(^|-)([a-z])/g, (_, prefix, letter) => prefix + letter.toUpperCase()), second!]] }),
    scenario(`response-probe-${name}-empty-after`, frame('message_stop', '{}'), { headers: [[name!, first!], [name!, '']] }),
  ]),
  ...[204, 205].map(status => scenario(`http-${status}-bodyless-stream`, '', { status })),
  ...[400, 401, 403, 404, 409, 422, 429, 500, 418].map(status => scenario(`http-${status}-sdk-api-error`, '{"type":"error","error":{"message":"nested"},"message":"top","future":true}', { status, headers: [['request-id', 'http-fixture']] })),
  scenario('http-invalid-json-body', '{bad', { status: 400 }),
  scenario('http-null-body', 'null', { status: 400 }),
  scenario('http-empty-body', '', { status: 400 }),
  scenario('http-false-body', 'false', { status: 400 }),
  ...['', '\n', '\r', '\r\n', '\n\n', '\r\r', '\r\n\r\n', '\n\r'].map(end => scenario(`eof-${JSON.stringify(end)}`, `event: message_stop\ndata: {"end":true}${end}`, { chunks: [1, 2, 3] })),
  scenario('mixed-line-delimiters-and-fragmented-crlf', 'event: completion\rdata: 1\r\nevent: message_stop\ndata: 2\n\r\n\n' + frame('completion', '3'), { chunks: [1, 3, 2, 4] }),
  scenario('bom-on-every-decoded-line', '\ufeffevent: completion\n\ufeffdata: {"text":"中文🙂"}\n\ufeff\n', { chunks: [1] }),
  { name: 'invalid-utf8-replacement-within-json-string', wireBytes: [...bytes('event: completion\ndata: {"text":"'), 0xf0, 0x9f, 0x22, 0x7d, 0x0a, 0x0a], chunks: [1] },
  { name: 'invalid-utf8-in-json-token-keeps-syntax-error', wireBytes: [...bytes('event: message_start\ndata: '), 0xf0, 0x90, 0x80, 0x0a, 0x0a], chunks: [2, 1] },
]
export const sdkStreamPendingScenarios: SDKStreamScenario[] = [
  scenario('connection-close-transport-metadata-pending', frame('completion', '{}'), { headers: [['connection', 'close']] }),
]
