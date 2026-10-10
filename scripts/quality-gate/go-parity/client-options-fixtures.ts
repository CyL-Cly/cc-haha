import type { HeaderSpec } from './message-body-fixtures'

// Direct Anthropic construction after provider/auth-source selection. OAuth,
// apiKeyHelper execution, cloud transports and proxy fetching are separate.
export type DirectClientStep = {
  name: string
  env: Record<string, string>
  explicitApiKey?: string
  workload?: string
  model?: string
  headers?: HeaderSpec
  observeFetch?: boolean
  beforeFetchEnv?: Record<string, string>
  observeRequestIDs?: boolean
}
export type DirectClientInput = {
  homeDir: string
  steps: DirectClientStep[]
  runtime: { platform: string; arch: string; version: string }
  resolvers?: ClientResolverCase[]
}
export const directClientEnvironmentNames = new Set([
  'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_CUSTOM_HEADERS', 'API_TIMEOUT_MS',
  'USER_TYPE', 'CLAUDE_CODE_ENTRYPOINT', 'CLAUDE_AGENT_SDK_VERSION', 'CLAUDE_AGENT_SDK_CLIENT_APP',
  'CLAUDE_CODE_CONTAINER_ID', 'CLAUDE_CODE_REMOTE_SESSION_ID', 'CLAUDE_CODE_ADDITIONAL_PROTECTION',
  'CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST', 'CC_HAHA_LOCAL_ACCESS_TOKEN',
])
export const clientResolverEnvironmentNames = new Set([...directClientEnvironmentNames,
  'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI',
])
export type ClientResolverCase = { name: string; env: Record<string, string>; requestURL: string }
const proxyBase = 'http://127.0.0.1:43210/proxy/providers/fixture'
const proxyEnv = { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: proxyBase, CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }
export const clientResolverCases: ClientResolverCase[] = [
  ...[
    '', proxyBase, `${proxyBase}/v1/messages?beta=true`, `${proxyBase}-other/v1/messages`, `${proxyBase}/../other/v1/messages`,
    `${proxyBase}/%2e%2e/other/v1/messages`, `${proxyBase}%2Fother/v1/messages`, `${proxyBase}/v1/messages#fragment`,
    'http://127.0.0.1:43211/proxy/providers/fixture/v1/messages', 'https://127.0.0.1:43210/proxy/providers/fixture/v1/messages',
    'http://user@127.0.0.1:43210/proxy/providers/fixture/v1/messages', 'http://:password@127.0.0.1:43210/proxy/providers/fixture/v1/messages',
    'http://127.1:43210/proxy/providers/fixture/v1/messages', 'http://0x7f000001:43210/proxy/providers/fixture/v1/messages', 'invalid-url',
  ].map(requestURL => ({ name: `managed requested URL ${JSON.stringify(requestURL)}`, env: proxyEnv, requestURL })),
  ...[
    '', 'invalid-url', 'https://127.0.0.1:43210/proxy', 'http://localhost:43210/proxy', 'http://[::1]:43210/proxy',
    'http://127.1:43210/proxy', 'http://0x7f000001:43210/proxy', 'http://127.0.0.1:43210/',
    'http://127.0.0.1:43210/proxy///', 'http://127.0.0.1:43210/proxy/providers/fixture/extra',
    'http://user@127.0.0.1:43210/proxy', 'http://:password@127.0.0.1:43210/proxy',
    'http://127.0.0.1:43210/proxy?', 'http://127.0.0.1:43210/proxy?query', 'http://127.0.0.1:43210/proxy#', 'http://127.0.0.1:43210/proxy#fragment',
  ].map(base => ({ name: `managed base ${JSON.stringify(base)}`, env: { ...proxyEnv, ANTHROPIC_BASE_URL: base }, requestURL: base })),
  ...[
    { CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'false' }, { ANTHROPIC_API_KEY: 'real-provider-key' }, { CC_HAHA_LOCAL_ACCESS_TOKEN: '' },
    { CC_HAHA_LOCAL_ACCESS_TOKEN: '\ufeff \u2028' }, { CC_HAHA_LOCAL_ACCESS_TOKEN: '\u0085token\u0085' },
  ].map(overrides => ({ name: `managed guard ${JSON.stringify(overrides)}`, env: { ...proxyEnv, ...overrides }, requestURL: proxyBase })),
  ...[
    {}, { ANTHROPIC_BASE_URL: 'https://API.ANTHROPIC.COM:443' }, { ANTHROPIC_BASE_URL: 'https://api.anthropic.com:8443' },
    { ANTHROPIC_BASE_URL: 'https://api-staging.anthropic.com' }, { ANTHROPIC_BASE_URL: 'https://api-staging.anthropic.com', USER_TYPE: 'ant' },
    { ANTHROPIC_BASE_URL: 'invalid-url' }, ...['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI'].map(name => ({ [name]: 'true' })),
  ].map(env => ({ name: `first party decision ${JSON.stringify(env)}`, env, requestURL: '' })),
]
const step = (name: string, env: Record<string, string> = {}, rest: Omit<DirectClientStep, 'name' | 'env'> = {}): DirectClientStep => ({
  name, env: { ANTHROPIC_API_KEY: 'offline-client-key', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210', ...env }, ...rest,
})
export const directClientSteps: DirectClientStep[] = [
  step('default direct profile'),
  step('explicit API key wins', { ANTHROPIC_AUTH_TOKEN: 'env-token' }, { explicitApiKey: 'explicit-key' }),
  step('empty explicit key falls back', {}, { explicitApiKey: '' }),
  step('auth token without API key', { ANTHROPIC_API_KEY: '', ANTHROPIC_AUTH_TOKEN: 'env-token' }),
  step('both environment credentials', { ANTHROPIC_AUTH_TOKEN: 'env-token' }),
  step('empty token constructor behavior', { ANTHROPIC_API_KEY: '', ANTHROPIC_AUTH_TOKEN: '' }),
  step('missing credentials fail before fetch', { ANTHROPIC_API_KEY: '' }),
  step('API key spaces are retained until Headers', { ANTHROPIC_API_KEY: '  offline-key  ' }),
  step('token constructor trim differs from app header', { ANTHROPIC_AUTH_TOKEN: '  token  ' }),
  step('session and remote SDK metadata', { USER_TYPE: 'internal', CLAUDE_CODE_ENTRYPOINT: 'sdk', CLAUDE_AGENT_SDK_VERSION: '0.2.3', CLAUDE_AGENT_SDK_CLIENT_APP: 'client/1.0', CLAUDE_CODE_CONTAINER_ID: 'container-id', CLAUDE_CODE_REMOTE_SESSION_ID: 'remote-id' }, { workload: 'cron' }),
  step('empty entrypoint is retained', { CLAUDE_CODE_ENTRYPOINT: '' }),
  step('empty optional metadata is absent', { USER_TYPE: '', CLAUDE_AGENT_SDK_VERSION: '', CLAUDE_AGENT_SDK_CLIENT_APP: '', CLAUDE_CODE_CONTAINER_ID: '', CLAUDE_CODE_REMOTE_SESSION_ID: '' }),
  step('protection truthy trimmed BOM', { CLAUDE_CODE_ADDITIONAL_PROTECTION: '\ufeff On \u2028' }),
  step('protection false text', { CLAUDE_CODE_ADDITIONAL_PROTECTION: 'false' }),
  step('custom header exact and case duplicates', { ANTHROPIC_CUSTOM_HEADERS: 'User-Agent: exact\nuser-agent: later\nX-Future: first\nX-Future: replacement\nx-future: final\n__proto__: ignored\nconstructor: present\n2: two\n1: one' }),
  step('custom multiline whitespace and first colon', { ANTHROPIC_CUSTOM_HEADERS: '\r\ninvalid\n : ignored\n\ufeff X-Future :\u2028 value:with:colons \ufeff\r\nX-Empty: \t\r\n' }),
  step('environment token overrides custom authorization', { ANTHROPIC_AUTH_TOKEN: 'environment', ANTHROPIC_CUSTOM_HEADERS: 'Authorization: Bearer custom\nX-Api-Key: custom-key\nContent-Type: custom/type' }),
  step('SDK body content type overrides client default', { ANTHROPIC_CUSTOM_HEADERS: 'Content-Type: custom/type\nAccept: custom/accept\nAnthropic-Version: custom-version' }),
  step('request headers override all defaults', { ANTHROPIC_CUSTOM_HEADERS: 'X-Future: client' }, { headers: { kind: 'object', entries: [['X-Api-Key', 'request-key'], ['Authorization', 'Bearer request-token'], ['User-Agent', 'request-agent'], ['x-app', 'request-app'], ['X-Future', ['first', 'second']], ['anthropic-dangerous-direct-browser-access', null], ['x-stainless-lang', 'request-lang']] } }),
  step('explicit null authentication permits omission', {}, { headers: { kind: 'object', entries: [['X-Api-Key', null], ['Authorization', null], ['Content-Type', null], ['Anthropic-Version', null]] } }),
  step('explicit empty authentication fails', {}, { headers: { kind: 'object', entries: [['X-Api-Key', ''], ['Authorization', '']] } }),
  step('undefined authentication retains defaults', {}, { headers: { kind: 'object', entries: [], undefinedNames: ['X-Api-Key', 'Authorization'] } }),
  step('undefined overwrites earlier omission and value', {}, { headers: { kind: 'object', entries: [['X-Api-Key', null], ['Authorization', 'Bearer shadow'], ['X-Future', 'shadow']], undefinedNames: ['X-Api-Key', 'Authorization', 'X-Future'] } }),
  step('gateway v1 normalization', { ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/gateway/v1///' }),
  step('gateway terminal slash retained', { ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/gateway///' }),
  step('official DeepSeek Anthropic route', { ANTHROPIC_BASE_URL: 'https://api.deepseek.com/anthropic' }, { model: 'deepseek-flash' }),
  step('first party generates request ID', { ANTHROPIC_BASE_URL: 'https://api.anthropic.com' }, { observeRequestIDs: true }),
  step('first party preserves supplied empty request ID', { ANTHROPIC_BASE_URL: 'https://api.anthropic.com' }, { observeRequestIDs: true, headers: { kind: 'object', entries: [['X-Client-Request-Id', '']] } }),
  step('first party explicit null request ID generates new', { ANTHROPIC_BASE_URL: 'https://api.anthropic.com' }, { observeRequestIDs: true, headers: { kind: 'object', entries: [['x-client-request-id', null]] } }),
  step('external user staging base has no request ID', { ANTHROPIC_BASE_URL: 'https://api-staging.anthropic.com' }, { observeRequestIDs: true }),
  step('ant user staging base generates request ID', { ANTHROPIC_BASE_URL: 'https://api-staging.anthropic.com', USER_TYPE: 'ant' }, { observeRequestIDs: true }),
  step('nondefault first party port has no request ID', { ANTHROPIC_BASE_URL: 'https://api.anthropic.com:8443' }, { observeRequestIDs: true }),
  step('managed loopback provider overrides caller authorization', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy/providers/fixture', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: ' yes ', CC_HAHA_LOCAL_ACCESS_TOKEN: '\ufeff local-token \u2028' }, { headers: { kind: 'object', entries: [['Authorization', 'Bearer caller']] } }),
  step('managed root proxy supplies explicitly omitted auth', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }, { headers: { kind: 'object', entries: [['authorization', null], ['x-api-key', null]] } }),
  step('managed token rotation reads final environment', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy/providers/fixture', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'old-token' }, { beforeFetchEnv: { CC_HAHA_LOCAL_ACCESS_TOKEN: 'new-token' } }),
  step('changed final base prevents credential injection', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy/providers/fixture', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }, { beforeFetchEnv: { ANTHROPIC_BASE_URL: 'https://provider.example/proxy' } }),
  step('managed token does not reach remote provider', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'https://provider.example/proxy/providers/fixture', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }),
  step('managed path extension is not an allowed base', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy/providers/fixture/extra', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }),
  step('managed whitespace token does not replace caller auth', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210/proxy', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: ' \ufeff ' }, { headers: { kind: 'object', entries: [['authorization', 'Bearer caller']] } }),
  step('managed localhost alias is not the accepted host', { ANTHROPIC_API_KEY: 'proxy-managed', ANTHROPIC_BASE_URL: 'http://localhost:43210/proxy', CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST: 'true', CC_HAHA_LOCAL_ACCESS_TOKEN: 'local-token' }),
  ...['', '1250ms', '  +42.9 ', '0x20', '1e3', '-1', 'not-a-number', '\ufeff00123', '2147483648', '9'.repeat(320)].map(value => step(`constructor timeout ${JSON.stringify(value)}`, { API_TIMEOUT_MS: value }, { observeFetch: false })),
]
