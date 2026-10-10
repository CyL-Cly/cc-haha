export type RequestRetryMode = 'sequence' | 'pause' | 'stop-notice' | 'abort-sleep' | 'abort-before' | 'abort-headers' | 'stop-stream' | 'overflow'
export type RequestRetryInput = {
  name: string
  mode: RequestRetryMode
  home: string
  env: Record<string, string>
  historySources: string[]
  systemSources: string[]
  model: string
  maxOutputTokens: number
  deviceID: string
  sessionID: string
  runtime: { platform: string; arch: string; version: string }
  sdkBetas?: string[]
  nonInteractive?: boolean
  querySource?: string
  toolDefinitions?: import('./tool-schema-fixtures').ToolDefinition[]
  extraToolSchemaSources?: string[]
  toolChoiceSource?: string
  auth?: { flagSettingsPath?: string; flagSettingsInline?: unknown }
}
export const requestRetryProfile = {
  historySources: [JSON.stringify({ type: 'user', message: { role: 'user', content: '01234567 independent physical creation retry' }, uuid: 'original-user', timestamp: '2023-11-14T22:13:20.000Z' })],
  systemSources: ['"private fixture system"'],
  model: 'deepseek-flash', maxOutputTokens: 32000,
  deviceID: 'fixture-retry-device', sessionID: '00000000-0000-4000-8000-000000000000',
}
export type RequestRetryCase = Partial<Pick<RequestRetryInput, 'historySources' | 'systemSources' | 'model' | 'maxOutputTokens' | 'sdkBetas'>> & { name: string; mode: RequestRetryMode; extraEnv?: Record<string, string> }
export const requestRetryCases: RequestRetryCase[] = [
  ...(['sequence', 'pause', 'stop-notice', 'abort-sleep', 'abort-before', 'abort-headers', 'stop-stream'] as const).map(mode => ({ name: mode, mode })),
  { name: 'original beta suffix and frozen model', mode: 'sequence' as const, model: 'claude-sonnet-4-6[1m]', sdkBetas: ['fixture-explicit-beta'] },
  { name: 'orphan tool before final user prepared once', mode: 'sequence' as const,
    historySources: [JSON.stringify({ type: 'assistant', message: { id: 'old-message', type: 'message', role: 'assistant', model: 'deepseek-flash', content: [{ type: 'tool_use', id: 'orphan-id', name: 'HistoricalTool', input: { future: 'original' } }], stop_reason: 'tool_use', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 2 } }, uuid: 'original-assistant', timestamp: '2023-11-14T22:13:20.000Z' }), ...requestRetryProfile.historySources],
  },
  { name: 'orphan tool missing final user prepared once', mode: 'sequence' as const,
    historySources: [JSON.stringify({ type: 'assistant', message: { id: 'old-message', type: 'message', role: 'assistant', model: 'deepseek-flash', content: [{ type: 'tool_use', id: 'orphan-id', name: 'HistoricalTool', input: { future: 'original' } }], stop_reason: 'tool_use', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 2 } }, uuid: 'original-assistant', timestamp: '2023-11-14T22:13:20.000Z' })],
  },
  { name: 'HTTP context overflow retains once-prepared orphan history', mode: 'overflow' as const,
    historySources: [JSON.stringify({ type: 'assistant', message: { id: 'old-message', type: 'message', role: 'assistant', model: 'deepseek-flash', content: [{ type: 'tool_use', id: 'orphan-id', name: 'HistoricalTool', input: { future: 'original' } }], stop_reason: 'tool_use', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 2 } }, uuid: 'original-assistant', timestamp: '2023-11-14T22:13:20.000Z' })],
  },
  { name: 'original UTF16 and rounded metadata', mode: 'sequence' as const,
    historySources: [JSON.stringify({ type: 'user', message: { role: 'user', content: '01234567 \ud800 independent \udc00' }, uuid: 'original-user', timestamp: '2023-11-14T22:13:20.000Z' })],
    extraEnv: { CLAUDE_CODE_EXTRA_METADATA: '{"z":1,"future":9007199254740993,"tail":"\\ud800"}' },
  },
]
export const requestRetryErrorBody = (status: number) => status === 400 ? '{"message":"input length and `max_tokens` exceed context limit: 20000 + 32000 > 50000"}' : `{"message":"creation retry ${status}","future":9007199254740993,"text":"<>&\\ud800"}`
export const requestRetryStart = 'event: message_start\ndata: {"type":"message_start","message":{"id":"retry-message","type":"message","role":"assistant","model":"deepseek-flash","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":2,"output_tokens":0}}}\n\n'
export const requestRetryWire = requestRetryStart
  + 'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n'
  + 'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"independent success"}}\n\n'
  + 'event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n'
  + 'event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}\n\n'
  + 'event: message_stop\ndata: {"type":"message_stop"}\n\n'
