export type MappingMutation = { target: 'input' | 'output' | 'repeat'; path: Array<string | number>; operation: 'set' | 'append'; key?: string | number; value: unknown }
export type RequestMappingStep = {
  operation: 'user' | 'assistant' | 'breakpoints'
  message?: unknown
  messageSource?: string
  messages?: unknown[]
  addCache?: boolean
  enabled?: boolean
  skipCacheWrite?: boolean
  querySource?: string
  eligible?: boolean
  allowlist?: string[]
  cacheControl?: unknown
  mutations?: MappingMutation[]
  repeat?: boolean
}
export type RequestMappingRequest = { homeDir: string; steps: RequestMappingStep[] }
const envelope = (type: string, content: unknown) => ({ type, uuid: 'ignored-id', message: { role: 'ignored-role', content }, metadata: { ignored: true } })
const user = (content: unknown) => envelope('user', content)
const assistant = (content: unknown) => envelope('assistant', content)
const block = { type: 'text', text: 'before 中文\ud800', nested: { value: 'shared' } }

export const requestMappingScenarios: Array<[string, RequestMappingStep[]]> = [
  ['user noncache string passthrough and shallow array clone', [
    { operation: 'user', message: user('literal 中文🙂\ud800\udfff'), repeat: true },
    { operation: 'user', message: user([block]), repeat: true, mutations: [{ target: 'output', path: ['content'], operation: 'append', value: { type: 'text', text: 'output only' } }, { target: 'output', path: ['content', 0], operation: 'set', key: 'text', value: 'shared field' }] },
    { operation: 'user', message: user([block]), repeat: true, mutations: [{ target: 'output', path: ['content'], operation: 'set', key: 0, value: { type: 'text', text: 'output replacement' } }, { target: 'input', path: ['message', 'content'], operation: 'append', value: { type: 'text', text: 'input only' } }] },
  ]],
  ['assistant noncache shares original array and blocks', [
    { operation: 'assistant', message: assistant([block]), repeat: true, mutations: [{ target: 'output', path: ['content'], operation: 'append', value: { type: 'text', text: 'shared append' } }, { target: 'repeat', path: ['content', 0], operation: 'set', key: 'text', value: 'shared repeat field' }] },
    { operation: 'assistant', message: assistant('raw assistant string'), repeat: true },
  ]],
  ['cache mapper copies blocks but retains nested references', [
    { operation: 'user', message: user([block]), addCache: true, enabled: true, repeat: true, mutations: [{ target: 'output', path: ['content', 0], operation: 'set', key: 'text', value: 'isolated output' }, { target: 'output', path: ['content', 0, 'nested'], operation: 'set', key: 'value', value: 'shared nested' }, { target: 'input', path: ['message', 'content', 0], operation: 'set', key: 'text', value: 'input update' }] },
    { operation: 'assistant', message: assistant([block]), addCache: true, enabled: false, repeat: true, mutations: [{ target: 'output', path: ['content', 0], operation: 'set', key: 'text', value: 'isolated output' }, { target: 'repeat', path: ['content', 0, 'nested'], operation: 'set', key: 'value', value: 'shared repeat nested' }] },
  ]],
  ['cache strings become text blocks even when caching disabled', ['', '🙂\ud800', 'line\r\ntext'].flatMap(content => [
    { operation: 'user' as const, message: user(content), addCache: true, enabled: false },
    { operation: 'assistant' as const, message: assistant(content), addCache: true, enabled: true },
  ])],
  ['last thinking and redacted thinking do not receive markers', [
    ...['thinking', 'redacted_thinking'].map(type => ({ operation: 'assistant' as const, message: assistant([{ type: 'text', text: 'before' }, { type, thinking: 'private', signature: 'retain', cache_control: { type: 'existing', future: null } }]), addCache: true, enabled: true })),
    { operation: 'assistant', message: assistant([{ type: 'thinking', thinking: 'private' }, { type: 'text', text: 'last' }]), addCache: true, enabled: true },
    { operation: 'assistant', message: assistant([{ type: 'connector_text', text: 'connector feature disabled' }]), addCache: true, enabled: true },
  ]],
  ['unknown UTF16 fields values and existing controls survive', [
    { operation: 'user', message: user([{ type: 'text', text: 'hi\udfff', '\ud800': 'high', '\udfff': 'low', '�': 'replacement', future: [null, false, 0, '', { escaped: '\ud800' }], cache_control: { type: 'custom', ttl: false } }, { type: 'tool_result', tool_use_id: 'call', content: 'result', cache_reference: 'existing', future: { '\ud800': 'nested' } }]), addCache: true, enabled: true },
    { operation: 'assistant', message: assistant([{ type: 'future_block', '\ud800': null, '\udfff': false, text: '\ud800\udfff', cache_control: { ttl: 'retain', future: true } }]), addCache: true, enabled: false },
  ]],
  ['all-message marker placement order empty and short fork', [
    { operation: 'breakpoints', messages: [], enabled: true },
    { operation: 'breakpoints', messages: [user('one')], enabled: true, skipCacheWrite: true },
    { operation: 'breakpoints', messages: [user('one'), assistant([{ type: 'text', text: 'two' }]), user([{ type: 'tool_result', tool_use_id: 'call', content: 'three' }])], enabled: true },
    { operation: 'breakpoints', messages: [user('one'), assistant([{ type: 'text', text: 'two' }]), user('three')], enabled: true, skipCacheWrite: true },
    { operation: 'breakpoints', messages: [user('one'), assistant([{ type: 'thinking', thinking: 'two' }])], enabled: true },
    { operation: 'breakpoints', messages: [user('one'), assistant('two')], enabled: false },
  ]],
  ['only append marker and retain pre-existing marker counts', [
    { operation: 'breakpoints', messages: [user([{ type: 'text', text: 'existing', cache_control: { type: 'ephemeral', ttl: 'old' } }]), assistant([{ type: 'thinking', thinking: 'private' }, { type: 'text', text: 'new' }])], enabled: true, repeat: true },
    { operation: 'breakpoints', messages: [user([]), assistant([])], enabled: true },
    { operation: 'user', message: user([]), addCache: true, enabled: true },
  ]],
  ['actual bootstrap eligibility allowlist preserves CLI SDK sources', [
    ...['repl_main_thread', 'sdk', 'agent:fixture', '', undefined].map(querySource => ({ operation: 'user' as const, message: user('TTL'), addCache: true, enabled: true, querySource, eligible: true, allowlist: ['repl_main_thread*', 'sdk'], cacheControl: { type: 'ephemeral', ...(querySource === 'sdk' || querySource === 'repl_main_thread' ? { ttl: '1h' } : {}) } })),
    { operation: 'assistant', message: assistant('TTL wildcard'), addCache: true, enabled: true, eligible: true, allowlist: ['*'], querySource: 'agent:fixture', cacheControl: { type: 'ephemeral', ttl: '1h' } },
    { operation: 'assistant', message: assistant('TTL ineligible'), addCache: true, enabled: true, eligible: false, allowlist: ['*'], querySource: 'sdk' },
  ]],
  ['user array object-spread semantics retain primitive UTF16 indices', [
    { operation: 'user', message: user([null, true, 12, ['nested', false], '🙂\ud800', { type: 'text', text: 'last' }]), addCache: true, enabled: true },
    { operation: 'user', message: user([null]), addCache: true, enabled: true },
    { operation: 'assistant', message: assistant([null, false, '🙂', { type: 'text', text: 'last' }]), addCache: true, enabled: true },
    { operation: 'assistant', message: assistant([false]), addCache: true, enabled: true },
  ]],
  ['raw TypeErrors for missing malformed envelope or array content', [
    ...[null, {}, { message: null }, { message: {} }, { message: { content: null } }, { message: { content: false } }, { message: { content: 1 } }, { message: { content: {} } }].flatMap(message => [
      { operation: 'user' as const, message, addCache: true, enabled: true },
      { operation: 'assistant' as const, message, addCache: true, enabled: false },
    ]),
    { operation: 'assistant', message: assistant([null]), addCache: true, enabled: false },
    { operation: 'breakpoints', messages: [user('before'), null], enabled: true },
    { operation: 'breakpoints', messages: [user('fork marker'), { type: 'assistant', message: null }], enabled: true, skipCacheWrite: true },
    { operation: 'breakpoints', messages: [{ type: 'future', message: { content: 'assistant fallback' } }], enabled: true },
  ]],
  ['noncache malformed content keeps passthrough and missing fields', [null, false, 0, {}, undefined].flatMap(content => [
    { operation: 'user' as const, message: user(content), addCache: false },
    { operation: 'assistant' as const, message: assistant(content), addCache: false },
  ])],
  ['noncallable map property preserves Bun raw TypeError descriptions', [null, false, 1, 'bad', {}, [], '\ud800', 'a"b\\c\n中文<', 1e30].flatMap(map => [
    { operation: 'user' as const, message: { message: { content: { map } } }, addCache: true },
    { operation: 'assistant' as const, message: { message: { content: { map } } }, addCache: true },
  ])],
  ['raw JSON duplicate escaped keys numeric order and future values', [
    { operation: 'user', messageSource: '{"type":"user","message":{"content":[{"z":true,"a":0,"10":"ten","2":"two","\\ud800":"high","\\udfff":"low","\\u0061":"last a","type":"text","text":"\\ud800 < > & ","future":[false,null,"",0,{"z":1,"a":2}],"big":9007199254740993,"overflow":1e400}]}}', addCache: true, enabled: true },
    { operation: 'assistant', messageSource: '{"message":{"content":[{"cache_control":{"future":false},"type":"text","text":"before"}]}}', addCache: true, enabled: true },
  ]],
]
