import { createHash } from 'node:crypto'

export type H5NetworkInterface = {
  name: string
  entries: Array<{ address: string; family: string | number; internal: boolean }> | null
}
export type H5ServiceEnvironment = Partial<Record<'CLAUDE_H5_PUBLIC_BASE_URL' | 'CLAUDE_H5_AUTO_PUBLIC_URL', string | null>>
export type H5EffectiveUrlInput = {
  enabled: boolean
  storedPublicBaseUrl: string | null
  configuredPublicBaseUrl: string | null
  autoPublicBaseUrl: string | null
  localInterfaceHosts?: string[]
}
export type H5ServiceStep = {
  operation: 'read' | 'update' | 'enable' | 'disable' | 'regenerate' | 'validate-token' | 'origin' | 'diagnostics' | 'grace' | 'classify-url' | 'validate-url' | 'collect-hosts' | 'find-lan' | 'effective-url' | 'browser-safe-port'
  value?: unknown
  patch?: Record<string, unknown>
  unset?: string[]
  /** Zero-based step/results index. The referenced enable result must contain token. */
  tokenFrom?: number
  environment?: H5ServiceEnvironment
  interfaces?: H5NetworkInterface[]
  activePort?: number
  hosts?: string[]
  effective?: H5EffectiveUrlInput
}
export type H5ServiceRequest = {
  homeDir: string
  interfaces: H5NetworkInterface[]
  activePort: number
  environment: H5ServiceEnvironment
  steps: H5ServiceStep[]
}
export type H5ServiceResult = { value: unknown } | { error: { status: number; code: string; message: string } }
export type H5ServiceEffect = { operation: 'migration'; config: string } | { operation: 'write'; path: string; value: unknown }
export type H5ServiceOutput = {
  results: H5ServiceResult[]
  effects: H5ServiceEffect[]
  randomCalls: number[]
  interfaceCalls: number
  portCalls: number
  warningCount: number
  /** One real filesystem snapshot after each step. Paths are relative to HOME. */
  snapshots: Array<Record<string, { json: unknown } | { raw: string }>>
}
export type H5ServiceScenario = {
  files?: Record<string, string>
  interfaces?: H5NetworkInterface[]
  activePort?: number
  environment?: H5ServiceEnvironment
  steps: H5ServiceStep[]
}

export const defaultH5Interfaces: H5NetworkInterface[] = [
  { name: 'lo0', entries: [{ address: '127.0.0.1', family: 'IPv4', internal: true }] },
  { name: 'vEthernet (WSL)', entries: [{ address: '172.20.16.1', family: 'IPv4', internal: false }] },
  { name: 'Wi-Fi', entries: [{ address: '192.168.1.100', family: 'IPv4', internal: false }, { address: 'fe80::1234', family: 'IPv6', internal: false }] },
  { name: 'Ethernet', entries: [{ address: '10.0.0.5', family: 'IPv4', internal: false }] },
]
export const protectedUserSettings = '{"env":{"ANTHROPIC_API_KEY":"fake-protected-user-key","FUTURE_ENV":{"keep":true}},"futureUser":{"keep":[1,"中文 😀"]},"desktopTerminal":{"futureProfile":true}}\n'
const managedPath = '.claude/cc-haha/settings.json'
const customToken = 'my-custom-pinned-token'
const legacyToken = 'legacy-paired-token'
const tokenHash = (token: string) => createHash('sha256').update(token).digest('hex')
const withStored = (value: unknown, steps: H5ServiceStep[]): H5ServiceScenario => ({ files: { [managedPath]: JSON.stringify({ env: { FAKE_MANAGED_KEY: 'keep' }, futureRoot: { keep: true }, h5Access: value }) }, steps })
const network = (name: string, address: string, family: string | number = 'IPv4', internal = false): H5NetworkInterface => ({ name, entries: [{ address, family, internal }] })
const effective = (patch: Partial<H5EffectiveUrlInput>): H5ServiceStep => ({ operation: 'effective-url', effective: { enabled: true, storedPublicBaseUrl: null, configuredPublicBaseUrl: null, autoPublicBaseUrl: 'http://192.168.1.100:28670', ...patch } })

export const h5ServiceScenarios: Array<[string, H5ServiceScenario]> = [
  ['defaults, falsy shortcuts, normalized empty writes', { steps: [{ operation: 'validate-token' }, { operation: 'validate-token', value: null }, { operation: 'validate-token', value: '' }, { operation: 'origin' }, { operation: 'origin', value: null }, { operation: 'origin', value: '' }, { operation: 'read' }, { operation: 'diagnostics' }, { operation: 'grace' }, { operation: 'update', patch: {} }, { operation: 'disable' }, { operation: 'read' }] }],
  ['recoverable enable, disable, reenable and rotation lifecycle', withStored({ futureH5Child: { intentionallyRemovedOnWrite: true } }, [{ operation: 'enable' }, { operation: 'validate-token', tokenFrom: 0 }, { operation: 'enable' }, { operation: 'disable' }, { operation: 'validate-token', tokenFrom: 0 }, { operation: 'enable' }, { operation: 'validate-token', tokenFrom: 0 }, { operation: 'regenerate' }, { operation: 'validate-token', tokenFrom: 0 }, { operation: 'validate-token', tokenFrom: 7 }, { operation: 'read' }, { operation: 'diagnostics' }])],
  ['hand edited plaintext outranks stored hash and preview', withStored({ enabled: true, token: customToken, tokenHash: 'f'.repeat(64), tokenPreview: 'stale', allowedOrigins: ['https://EXAMPLE.com:443/path', 'https://example.com', null, 'https://*.example.com'], fixedPort: 28670, disconnectGraceSeconds: 600 }, [{ operation: 'read' }, { operation: 'validate-token', value: customToken }, { operation: 'origin', value: 'https://example.com/other?q=x' }, { operation: 'grace' }, { operation: 'disable' }, { operation: 'enable' }, { operation: 'validate-token', value: customToken }, { operation: 'regenerate' }, { operation: 'validate-token', value: customToken }])],
  ['legacy hash only stays enabled until explicit token replacement', withStored({ enabled: true, tokenHash: tokenHash(legacyToken), tokenPreview: 'legacy...oken', allowedOrigins: ['https://example.com'] }, [{ operation: 'read' }, { operation: 'validate-token', value: legacyToken }, { operation: 'origin', value: 'https://example.com' }, { operation: 'disable' }, { operation: 'validate-token', value: legacyToken }, { operation: 'enable' }, { operation: 'validate-token', value: legacyToken }, { operation: 'validate-token', tokenFrom: 5 }])],
  ['malformed persisted fields are read only sanitized then replaced', withStored({ enabled: 'true', token: 'short', tokenHash: 'F'.repeat(64), tokenPreview: 5, allowedOrigins: ['https://valid.example/path', 5, {}, 'file:///', 'http://user:pass@example.com', 'no-url', 'http://*.example'], publicBaseUrl: 7, fixedPort: 5061, disconnectGraceSeconds: 4, futureH5Child: 'drop' }, [{ operation: 'read' }, { operation: 'diagnostics' }, { operation: 'grace' }, { operation: 'validate-token', value: 'short' }, { operation: 'update', patch: {}, unset: ['allowedOrigins', 'publicBaseUrl', 'fixedPort', 'disconnectGraceSeconds'] }, { operation: 'read' }])],
  ['origin canonicalization, deduplication and enabled matching', { steps: [{ operation: 'update', patch: { allowedOrigins: ['HTTP://Example.COM:80/path?q=x#f', 'http://example.com', 'https://中文.example/path', 'http://0x7f000001:3000/a', 'http://[::1]:8080/path'] } }, { operation: 'origin', value: 'http://example.com/path' }, { operation: 'enable' }, ...['http://example.com/elsewhere', 'https://中文.example/路径', 'http://127.0.0.1:3000/', 'http://[::1]:8080/a', 'http://user:pass@example.com', 'http://*.example.com', 'file:///', 'not a URL', 'https://other.example'].map(value => ({ operation: 'origin' as const, value })), { operation: 'read' }] }],
  ['origin rejection is atomic and preserves managed fields', withStored({ enabled: true, token: customToken, allowedOrigins: ['https://keep.example'] }, [null, 'https://example.com', {}, [7], ['https://*.example.com'], ['not a URL'], ['ftp://example.com'], ['http://user@example.com'], ['http://:password@example.com']].map(allowedOrigins => ({ operation: 'update', patch: { allowedOrigins } })))],
  ['public URL normalization preserves proxy path and removes query fragment', { steps: [...['https://EXAMPLE.com:443/app///?token=drop#fragment', 'https://中文.example/a/../%2f/b/', 'http:\\example.com\\h5\\', 'http://127.0.0.1:80/', 'http://[::1]:8080/h5/', '', null].map(publicBaseUrl => ({ operation: 'update' as const, patch: { publicBaseUrl } })), { operation: 'read' }] }],
  ['public URL rejection before later fields and before writes', { steps: [...[7, true, {}, 'no URL', 'ftp://example.com', 'http://user:pass@example.com', 'http://192.168.255.254:28670'].map(publicBaseUrl => ({ operation: 'update' as const, patch: { publicBaseUrl, fixedPort: 80, disconnectGraceSeconds: 1, allowedOrigins: null } })), { operation: 'read' }] }],
  ['current LAN binding accepts canonical host, stale host errors retain list order', { steps: [{ operation: 'update', patch: { publicBaseUrl: 'http://192.168.1.100:55379///' } }, { operation: 'update', patch: { publicBaseUrl: 'http://10.0.0.5:80/?q=x' } }, { operation: 'update', patch: { publicBaseUrl: 'http://169.254.3.4:8080' } }, { operation: 'read' }, { operation: 'update', patch: { publicBaseUrl: 'http://192.168.255.254' }, interfaces: [] }] }],
  ['fixed port and disconnect grace validation order', { steps: [{ operation: 'update', patch: { fixedPort: 80, disconnectGraceSeconds: 1, allowedOrigins: null } }, { operation: 'update', patch: { fixedPort: 28670, disconnectGraceSeconds: 1, allowedOrigins: null } }, { operation: 'update', patch: { fixedPort: 28670, disconnectGraceSeconds: 5, allowedOrigins: null } }, { operation: 'read' }] }],
  ['fixed ports including every high browser blocked entry', { steps: [...[1024, 65535, null, 1023, 65536, 3456.5, '28670', true, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080].map(fixedPort => ({ operation: 'update' as const, patch: { fixedPort } })), { operation: 'read' }] }],
  ['disconnect grace floor ceiling null and rejected types', { steps: [{ operation: 'grace' }, ...[5, 86400, 4, 86401, 5.5, '600', false, null].flatMap(disconnectGraceSeconds => [{ operation: 'update' as const, patch: { disconnectGraceSeconds } }, { operation: 'grace' as const }]), { operation: 'read' }] }],
  ['configured and auto URL affect enabled public values without replacing storage', withStored({ token: customToken, enabled: false, publicBaseUrl: 'http://127.0.0.1:5179' }, [{ operation: 'read', environment: { CLAUDE_H5_PUBLIC_BASE_URL: 'https://public.example/app///?drop=1', CLAUDE_H5_AUTO_PUBLIC_URL: '1' } }, { operation: 'enable' }, { operation: 'diagnostics' }, { operation: 'read', environment: { CLAUDE_H5_PUBLIC_BASE_URL: null }, activePort: 39876 }, { operation: 'diagnostics' }, { operation: 'read', environment: { CLAUDE_H5_PUBLIC_BASE_URL: 'ftp://invalid.example' } }, { operation: 'disable' }, { operation: 'diagnostics' }])],
  ['auto mode physical LAN preference and all absent interfaces', { environment: { CLAUDE_H5_AUTO_PUBLIC_URL: '1' }, steps: [{ operation: 'enable' }, { operation: 'diagnostics' }, { operation: 'read', interfaces: [] }, { operation: 'diagnostics' }, { operation: 'read', environment: { CLAUDE_H5_AUTO_PUBLIC_URL: 'true' } }, { operation: 'read', environment: { CLAUDE_H5_AUTO_PUBLIC_URL: '0' } }] }],
  ['actual managed migration precedes H5 read and preserves backup and user bytes', { files: { [managedPath]: '{"env":["legacy-invalid-shape"],"futureRoot":{"keep":true},"h5Access":{"enabled":true,"token":"my-custom-pinned-token","allowedOrigins":["https://example.com"]}}' }, steps: [{ operation: 'read' }, { operation: 'enable' }, { operation: 'origin', value: 'https://example.com' }, { operation: 'read' }] }],
  ['corrupt managed JSON recovery precedes enable', { files: { [managedPath]: '{"env":' }, steps: [{ operation: 'read' }, { operation: 'enable' }, { operation: 'validate-token', tokenFrom: 1 }, { operation: 'read' }] }],
  ['managed array migration creates backup before normalized H5 writes', { files: { [managedPath]: '["unexpected",{"future":true}]' }, steps: [{ operation: 'read' }, { operation: 'update', patch: { disconnectGraceSeconds: 30 } }, { operation: 'grace' }] }],
  ['blank managed JSON is recovered by actual migration', { files: { [managedPath]: ' \n\t' }, steps: [{ operation: 'read' }, { operation: 'disable' }, { operation: 'read' }] }],
  ['legacy token preview keeps lone UTF16 through read and disable', { files: { [managedPath]: `{"futureRoot":{"keep":true},"h5Access":{"enabled":true,"tokenHash":"${tokenHash(legacyToken)}","tokenPreview":"A\\ud800B\\udfffC"}}` }, steps: [{ operation: 'read' }, { operation: 'validate-token', value: legacyToken }, { operation: 'disable' }, { operation: 'read' }, { operation: 'enable' }] }],
  ['managed future numeric and UTF16 fields follow actual JS parse then write', { files: { [managedPath]: '{"futureRoot":{"integer":9007199254740993,"huge":1e400,"negativeHuge":-1e400,"tiny":1e-400,"negativeZero":-0,"high":"\\ud800","nested":{"2":"two","z":1,"1":"one","z":2}},"h5Access":{"enabled":false,"token":"my-custom-pinned-token"}}' }, steps: [{ operation: 'read' }, { operation: 'enable' }, { operation: 'update', patch: { allowedOrigins: ['https://example.com'] } }, { operation: 'disable' }, { operation: 'read' }] }],
  ['managed lone UTF16 keys remain distinct from each other and replacement rune', { files: { [managedPath]: '{"\\ud800":{"kind":"high","integer":9007199254740993},"\\udfff":{"kind":"low","huge":1e400},"�":{"kind":"replacement"},"\\u0061":{"before":true},"a":{"after":true},"futureNested":{"\\ud800":"nested-high","\\udfff":"nested-low","ordinary":{"keep":true}},"h5Access":{"enabled":false}}' }, steps: [{ operation: 'read' }, { operation: 'enable' }, { operation: 'update', patch: { disconnectGraceSeconds: 600 } }, { operation: 'disable' }, { operation: 'read' }] }],
  ['equivalent escaped UTF16 keys replace last value without merging other keys', { files: { [managedPath]: '{"x\\ud800":{"value":"first"},"x\\uD800":{"value":"last"},"x\\udfff":{"value":"low"},"😀":{"value":"first-emoji"},"\\ud83d\\ude00":{"value":"last-emoji"},"futureNested":{"ordinary":"keep","duplicate":1,"\\u0064uplicate":2},"h5Access":{"enabled":false}}' }, steps: [{ operation: 'read' }, { operation: 'enable' }, { operation: 'disable' }, { operation: 'read' }] }],
  ['pure browser-safe port complete source bad-port table and adjacent bounds', { steps: [0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080, -1, 2, 80, 1024, 65535, 65536, 28670, 3.5].map(value => ({ operation: 'browser-safe-port', value })) }],
  ['pure URL classification and reachability checks use real WHATWG canonicalization', { steps: ['http://192.168.1.100:28670', 'http://10.0.0.5', 'http://172.16.1.2', 'http://172.31.1.2', 'http://172.32.1.2', 'http://169.254.3.4', 'http://0xC0A80164:28670', 'http://0300.0250.1.0144', 'http://192.168.1.100:80/a/..', 'http://192.168.1.100:80///', 'http://192.168.1.100/%2f', 'https://192.168.1.100', 'http://localhost', 'http://[::1]', 'http://[::ffff:192.168.1.100]', 'https://中文.example/a', 'http://user:pass@example.com', 'ftp://example.com', 'bad url'].flatMap(value => [{ operation: 'classify-url' as const, value }, { operation: 'validate-url' as const, value }]) }],
  ['pure URL validation suggested host follows preference then original order', { steps: [{ operation: 'validate-url', value: 'bad', hosts: [] }, { operation: 'validate-url', value: 'http://192.168.255.254', hosts: ['169.254.1.1', '172.20.1.1', '10.2.1.1', '192.168.2.1'] }, { operation: 'validate-url', value: 'bad', hosts: ['169.254.1.1', '172.20.1.1', '10.2.1.1'] }, { operation: 'validate-url', value: 'bad', hosts: ['169.254.1.1', '172.20.1.1'] }, { operation: 'validate-url', value: 'bad', hosts: ['203.0.113.1', '169.254.1.1'] }] }],
  ['pure effective URL priority, stale binding, port refresh and reverse proxies', { steps: [effective({ enabled: false, storedPublicBaseUrl: 'http://127.0.0.1:5179', configuredPublicBaseUrl: 'https://configured.example' }), effective({ configuredPublicBaseUrl: 'https://configured.example' }), effective({ storedPublicBaseUrl: 'http://192.168.1.100:5179', autoPublicBaseUrl: null }), ...['http://localhost:5179', 'http://0.0.0.0:5179', 'http://[::]:5179', 'http://[::1]:5179', 'http://127.0.0.1:5179'].map(storedPublicBaseUrl => effective({ storedPublicBaseUrl })), effective({ storedPublicBaseUrl: 'http://192.168.255.254:5179', localInterfaceHosts: ['192.168.1.100'] }), effective({ storedPublicBaseUrl: 'http://192.168.255.254:5179' }), effective({ storedPublicBaseUrl: 'http://192.168.1.100:5179', localInterfaceHosts: ['192.168.1.100'] }), effective({ storedPublicBaseUrl: 'http://192.168.1.100:5179', autoPublicBaseUrl: 'http://10.0.0.5:80' }), ...['https://public.example/app', 'https://192.168.1.100:8443', 'http://192.168.1.100:8080/h5', 'invalid-stored'].map(storedPublicBaseUrl => effective({ storedPublicBaseUrl })), effective({ storedPublicBaseUrl: 'http://192.168.1.100:5179', autoPublicBaseUrl: 'invalid-auto' }), effective({ configuredPublicBaseUrl: 'literal-configured-value' })] }],
  ['network scoring, JS enumeration, ties, virtual boundaries and legacy invalid octets', { steps: [
    ...[
      [network('vEthernet (WSL)', '172.20.16.1'), network('Docker Desktop', '172.17.0.1'), network('Wi-Fi', '192.168.1.100')],
      [network('wifi0', '192.168.2.1'), network('Ethernet', '10.1.1.1')],
      [network('Wi-Fi VPN', '192.168.2.1'), network('unknown', '10.1.1.1')],
      [network('unknown', '169.254.1.1'), network('Ethernet', '172.16.1.1')],
      [network('unknownA', '10.1.1.1'), network('unknownB', '10.2.2.2')],
      [network('2', '10.2.2.2'), network('1', '10.1.1.1'), network('01', '10.3.3.3')],
      [network('en0', '192.168.999.999'), network('Ethernet', '10.1.1.1')],
      [network('eth0', '203.0.113.1'), network('lo0', '127.0.0.1', 'IPv4', true), network('bad-family', '192.168.1.1', 4), network('ipv6', '::1', 'IPv6')],
      [{ name: 'absent', entries: null }],
      [],
    ].flatMap(interfaces => [{ operation: 'collect-hosts' as const, interfaces }, { operation: 'find-lan' as const }]),
  ] }],
  ...['http://192.168.1.100:55379', 'http://192.168.255.254:55379', 'https://proxy.example/app', 'invalid-url', null].map((publicBaseUrl): [string, H5ServiceScenario] => [`diagnostics staleness ${publicBaseUrl ?? 'unset'}`, { ...withStored({ enabled: true, token: customToken, publicBaseUrl }, [{ operation: 'read' }, { operation: 'diagnostics' }, { operation: 'diagnostics', activePort: 39876 }]), environment: { CLAUDE_H5_AUTO_PUBLIC_URL: '1' } }]),
  ...['!'.repeat(16), '~'.repeat(512), '!'.repeat(15), '!'.repeat(513), 'has space token value', 'token-with-中文-value', 'token-with-newline\n'].map((token, index): [string, H5ServiceScenario] => [`stored token syntax boundary ${index}`, withStored({ enabled: true, token, tokenHash: tokenHash(legacyToken), tokenPreview: 'legacy-preview' }, [{ operation: 'read' }, { operation: 'validate-token', value: token }, { operation: 'validate-token', value: legacyToken }, { operation: 'enable' }, { operation: 'read' }])]),
]
