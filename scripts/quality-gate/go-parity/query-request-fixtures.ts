export type QueryRequestCase = {
  name: string
  nonInteractive: boolean
  historySources: string[]
  systemSources: string[]
  maxOutputTokens: number
  env: Record<string, string>
  wire: string
}
export type QueryRequestInput = QueryRequestCase & {
  home: string
  origin: string
  runtime: { platform: string; arch: string; version: string }
  deviceID: string
  sessionID: string
}
export type QueryRequestObservation = {
  modelHistory: string[][]
  yielded: string[]
  yieldedAfter: string[]
  history: string[]
  allocations: Array<{ kind: 'uuid' | 'time'; value: string }>
  requests: Array<{ path: string; method: string; headers: Array<[string, string]>; body: string }>
  terminal: unknown
  error: null | { name: string; message: string }
}
const frame = (type: string, rest: Record<string, unknown>) => `event: ${type}\ndata: ${JSON.stringify({ type, ...rest })}\n\n`
export const queryRequestWire = frame('message_start', { message: { id: 'query-request-message', type: 'message', role: 'assistant', model: 'fixture-model', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 9, output_tokens: 0 } } }) + frame('content_block_start', { index: 0, content_block: { type: 'text', text: '' } }) + frame('content_block_delta', { index: 0, delta: { type: 'text_delta', text: 'complete' } }) + frame('content_block_stop', { index: 0 }) + frame('message_delta', { delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 3 } }) + frame('message_stop', {})
const user = (content: string) => JSON.stringify({ type: 'user', message: { role: 'user', content }, uuid: 'query-request-user', timestamp: '2023-11-14T22:13:20.000Z' })
export const queryRequestCases: QueryRequestCase[] = [
  { name: 'headless ordinary single round', nonInteractive: true, historySources: [user('Run one tool-free request')], systemSources: ['"Configured system"'], maxOutputTokens: 32000, env: {}, wire: queryRequestWire },
  { name: 'interactive ordinary single round', nonInteractive: false, historySources: [user('Run one tool-free request')], systemSources: ['"Configured system"'], maxOutputTokens: 32000, env: {}, wire: queryRequestWire },
  { name: 'headless original UTF16 and numeric history', nonInteractive: true, historySources: ['{"type":"user","message":{"role":"user","content":"0123\\ud80067\\udc00abcdefghijkl\\ud800 tail"},"uuid":"query-request-user","timestamp":"2023-11-14T22:13:20.000Z","future":{"z":9007199254740993,"a":"\\udfff"}}'], systemSources: ['"Configured\\ud800system"'], maxOutputTokens: 32000, env: {}, wire: queryRequestWire },
]
