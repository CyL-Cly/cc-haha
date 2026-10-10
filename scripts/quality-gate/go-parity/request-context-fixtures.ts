export type RequestContextCase = {
  name: string
  historySources: string[]
  systemSources?: string[]
  env?: Record<string, string>
  options?: Record<string, unknown>
  thinking?: { type: string; budgetTokens?: number }
  sdkBetas?: string[]
  ttlEligible?: boolean
  ttlAllowlist?: string[]
  workload?: string
  proxy?: boolean
}
export const requestContextEnvironmentNames = new Set([
  'CLAUDE_CODE_EXTRA_BODY', 'CLAUDE_CODE_EXTRA_METADATA', 'CLAUDE_CODE_MAX_OUTPUT_TOKENS', 'CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS',
  'CLAUDE_CODE_DISABLE_THINKING', 'CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING', 'CC_HAHA_SEND_DISABLED_THINKING',
  'DISABLE_PROMPT_CACHING', 'CLAUDE_CODE_ATTRIBUTION_HEADER', 'CLAUDE_CODE_ENTRYPOINT', 'ANTHROPIC_BETAS',
  'CLAUDE_CODE_ALWAYS_ENABLE_EFFORT', 'CLAUDE_CODE_EFFORT_LEVEL', 'CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS',
])
export function user(content: unknown, extra: Record<string, unknown> = {}) {
  return JSON.stringify({ type: 'user', message: { role: 'user', content }, uuid: 'user-original', timestamp: '2023-11-14T22:13:20.000Z', ...extra })
}
export function assistant(content: unknown[], id = 'assistant-original', extra: Record<string, unknown> = {}) {
  return JSON.stringify({ type: 'assistant', message: { id, model: 'fixture-model', role: 'assistant', content, stop_reason: 'end_turn', usage: { input_tokens: 0, output_tokens: 0 } }, uuid: id, timestamp: '2023-11-14T22:13:20.000Z', ...extra })
}
const basic = [user('01234567abcdefghijklmnopqrstuv')]
const item = (name: string, rest: Omit<RequestContextCase, 'name' | 'historySources'> & { historySources?: string[] } = {}): RequestContextCase => ({ name, historySources: basic, ...rest })
export const requestContextCases: RequestContextCase[] = [
  item('default relay'), item('local proxy default budget', { proxy: true }),
  item('explicit output cap', { options: { maxOutputTokensOverride: 2048 }, proxy: true }),
  item('zero override uses model default', { options: { maxOutputTokensOverride: 0 }, proxy: true }),
  item('provider budget', { env: { CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS: '123456' }, proxy: true }),
  item('global cap clamps native upper', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: '999999' }, proxy: true }),
  item('global positive one preserved', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: '1' }, proxy: true }),
  item('invalid global and provider budgets', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: 'wat', CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS: '-1' }, proxy: true }),
  item('extra body override cap and ordered fields', { env: { CLAUDE_CODE_EXTRA_BODY: '{"future":{"z":1,"a":2},"max_tokens":17,"model":"overridden","temperature":0}' }, proxy: true }),
  ...['null', '[]', 'true', '3', '"text"', '{'].map(source => item(`extra body ignored ${source}`, { env: { CLAUDE_CODE_EXTRA_BODY: source } })),
  item('metadata extras and reserved overwrite', { env: { CLAUDE_CODE_EXTRA_METADATA: '{"z":1,"2":"two","1":"one","device_id":"wrong","session_id":"wrong","account_uuid":"wrong","a":"\\ud800"}' } }),
  item('metadata nonobject ignored', { env: { CLAUDE_CODE_EXTRA_METADATA: '[1]' } }),
  item('custom system and append prefix', { systemSources: ['"first"', '""', '"second\\ud800"'], options: { hasAppendSystemPrompt: true } }),
  item('interactive prefix', { options: { isNonInteractiveSession: false }, systemSources: ['"system"'] }),
  item('system custom prefix override', { systemSources: ['"You are Claude Code, Anthropic\u0027s official CLI for Claude."', '"x-anthropic-billing-header: override; cch=00000;"', '"rest"'] }),
  item('attribution disabled', { env: { CLAUDE_CODE_ATTRIBUTION_HEADER: ' OFF ' } }),
  item('empty entrypoint preserved', { env: { CLAUDE_CODE_ENTRYPOINT: '' } }),
  item('workload attribution', { workload: 'cron', env: { CLAUDE_CODE_ENTRYPOINT: 'sdk' } }),
  item('fingerprint lone UTF16', { historySources: [user('0123\ud80067\udc00abcdefghijkl\ud800 tail')] }),
  item('cache explicitly disabled', { options: { enablePromptCaching: false }, systemSources: ['"system"'] }),
  item('cache environment disabled', { env: { DISABLE_PROMPT_CACHING: 'yes' } }),
  item('option cache true beats environment', { env: { DISABLE_PROMPT_CACHING: 'true' }, options: { enablePromptCaching: true } }),
  item('latched one-hour TTL', { ttlEligible: true, ttlAllowlist: ['sdk'] }),
  item('latched prefix TTL match', { ttlEligible: true, ttlAllowlist: ['repl*'], options: { querySource: 'repl_main_thread:custom' } }),
  item('latched TTL no match', { ttlEligible: true, ttlAllowlist: ['agent:*'] }),
  item('skip cache write with one message', { options: { skipCacheWrite: true } }),
  item('cache second last message', { historySources: [user('first'), assistant([{ type: 'text', text: 'answer' }]), user('last', { uuid: 'last-user' })], options: { skipCacheWrite: true } }),
  item('temperature zero preserved', { options: { temperatureOverride: 0 } }),
  item('unsupported model requested thinking omits temperature', { thinking: { type: 'enabled', budgetTokens: 4096 } }),
  item('manual thinking default budget', { options: { model: 'claude-sonnet-4-20250514' }, thinking: { type: 'adaptive' } }),
  item('manual thinking explicit capped budget', { options: { model: 'claude-sonnet-4-20250514', maxOutputTokensOverride: 2048 }, thinking: { type: 'enabled', budgetTokens: 99999 } }),
  item('thinking budget cannot fit', { options: { model: 'claude-sonnet-4-20250514', maxOutputTokensOverride: 1024 }, thinking: { type: 'enabled', budgetTokens: 4096 } }),
  item('thinking environment disabled', { options: { model: 'claude-opus-4-20250514' }, thinking: { type: 'enabled', budgetTokens: 2048 }, env: { CLAUDE_CODE_DISABLE_THINKING: 'true' } }),
  item('explicit disabled thinking opt in', { env: { CC_HAHA_SEND_DISABLED_THINKING: 'true' } }),
  item('required adaptive relay', { options: { model: 'claude-sonnet-5-5' } }),
  item('1m model and explicit betas', { options: { model: 'fixture-model[1M]' }, env: { ANTHROPIC_BETAS: 'one, two,one,, ' }, sdkBetas: ['two', 'sdk-beta'] }),
  item('experimental disabled retains explicit beta', { options: { model: 'fixture-model[1m]' }, env: { CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: 'true', ANTHROPIC_BETAS: 'selected' } }),
  item('output format', { options: { outputFormat: { type: 'json_schema', schema: { type: 'object', properties: { z: { type: 'string' } } } } } }),
  item('extra output format wins even null', { env: { CLAUDE_CODE_EXTRA_BODY: '{"output_config":{"format":null,"future":1}}' }, options: { outputFormat: { type: 'json_schema', schema: {} } } }),
  item('effort forced with explicit setting', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true' }, options: { effortValue: 'low' } }),
  item('extra effort wins', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_EXTRA_BODY: '{"output_config":{"effort":null}}' }, options: { effortValue: 'high' } }),
  item('extra tools and tool choice', { options: { extraToolSchemas: [{ type: 'web_search_20250305', name: 'web_search' }], toolChoice: { type: 'auto' } } }),
  item('consecutive users seam and meta UUID', { historySources: [user('first', { isMeta: true }), user('second', { uuid: 'second-user' })] }),
  item('virtual and progress omitted', { historySources: [user('virtual', { isVirtual: true }), JSON.stringify({ type: 'progress', content: 'ignored' }), ...basic] }),
  item('assistant same-id merge and trailing thinking', { historySources: [user('question'), assistant([{ type: 'thinking', thinking: 'private', signature: 'sig' }]), assistant([{ type: 'text', text: 'answer' }]), assistant([{ type: 'thinking', thinking: 'tail', signature: 'tail' }])] }),
  item('orphan thinking removed', { historySources: [user('first'), assistant([{ type: 'thinking', thinking: 'orphan', signature: 'sig' }]), user('next', { uuid: 'next' })] }),
  item('whitespace assistant removal merges users', { historySources: [user('first'), assistant([{ type: 'text', text: '\ufeff \u2028\n' }]), user('next', { uuid: 'next' })] }),
  item('empty nonfinal assistant placeholder', { historySources: [user('first'), assistant([]), user('next', { uuid: 'next' })] }),
  item('final empty assistant retained', { historySources: [user('first'), assistant([])] }),
  item('tool-use caller stripped and pairing retained', { historySources: [user('question'), assistant([{ future: 1, name: 'UnknownTool', type: 'tool_use', id: 'tool-id', input: { z: 1, a: '\ud800' }, caller: { type: 'direct' } }]), user([{ type: 'tool_result', tool_use_id: 'tool-id', content: 'done' }])] }),
  item('pairing repairs missing result', { historySources: [user('question'), assistant([{ type: 'tool_use', id: 'missing', name: 'UnknownTool', input: {} }]), user('next', { uuid: 'next' })] }),
  item('orphan tool result removed', { historySources: [user([{ type: 'tool_result', tool_use_id: 'orphan', content: 'old' }])] }),
  item('request chain original id', { historySources: [user('question'), assistant([{ type: 'text', text: 'answer' }], 'a', { requestId: 'earlier-request' }), user('next', { uuid: 'next' })] }),
  item('pairing allocates missing final user', { historySources: [user('question'), assistant([{ type: 'tool_use', id: 'missing', name: 'UnknownTool', input: {} }])] }),
  item('tool references removed from paired result', { historySources: [user('question'), assistant([{ type: 'tool_use', id: 'tool-id', name: 'UnknownTool', input: {} }]), user([{ type: 'tool_result', tool_use_id: 'tool-id', content: [{ type: 'tool_reference', tool_name: 'unknown' }] }])] }),
  item('tool references removed but ordinary text retained', { historySources: [user('question'), assistant([{ type: 'tool_use', id: 'tool-id', name: 'UnknownTool', input: {} }]), user([{ type: 'tool_result', tool_use_id: 'tool-id', content: [{ type: 'tool_reference', tool_name: 'unknown' }, { type: 'text', text: 'kept' }] }])] }),
  item('extra-body UTF16 keys numeric stringify and stream slot', { env: { CLAUDE_CODE_EXTRA_BODY: '{"\\ud800":1,"\\udc00":2,"�":3,"2":2,"1":1,"stream":false,"future":{"n":1e400,"zero":-0},"later":true}' } }),
  item('global parseInt decimal', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: '123.4' }, proxy: true }),
  item('global parseInt junk keeps provider override', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: '123junk', CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS: '555' }, proxy: true }),
  item('global Number hex explicit but parseInt decimal output', { env: { CLAUDE_CODE_MAX_OUTPUT_TOKENS: '0x20' }, proxy: true }),
  item('forced effort invalid env is ignored', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_EFFORT_LEVEL: 'banana' } }),
  item('forced numeric effort external omitted', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_EFFORT_LEVEL: '10' } }),
  item('forced effort request overrides environment', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_EFFORT_LEVEL: 'high' }, options: { effortValue: 'low', effortValueOverridesEnv: true } }),
  item('forced effort relay beta kill switch', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: 'true' } }),
  item('forced effort proxy survives beta kill switch', { env: { CLAUDE_CODE_ALWAYS_ENABLE_EFFORT: 'true', CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: 'true' }, proxy: true }),
  ...['claude-3-opus', 'claude-3-sonnet', 'claude-3-haiku', 'claude-3-5-sonnet', 'claude-3-5-haiku', 'claude-3-7-sonnet', 'claude-sonnet-4-5', 'claude-haiku-4-5', 'claude-opus-4-1', 'claude-opus-4-5', 'claude-sonnet-4-6', 'claude-opus-4-6', 'claude-opus-4-7', 'claude-opus-4-8', 'claude-opus-5', 'claude-sonnet-5', 'claude-opus-5-5', 'claude-fable-5-1'].map(model => item(`model default ${model}`, { options: { model } })),
  item('model default deepseek-flash original id and proxy path', { options: { model: 'deepseek-flash' }, proxy: true }),
]

export const requestContextWire = 'event: message_start\ndata: {"type":"message_start","message":{"id":"response","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n'
