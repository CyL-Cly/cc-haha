import type { H5NetworkInterface, H5ServiceEnvironment } from './h5-service-fixtures'

export type H5HttpStep = {
  method: string
  path: string
  segments?: string[]
  headers?: Array<[string, string]>
  body?: string
  tokenFrom?: number
  bearerPrefix?: string
  readError?: { name: string; message: string }
}
export type H5HttpFault = { operation: string; path: string; message: string; once?: boolean; afterManagedCommit?: boolean }
export type H5HttpScenario = {
  name: string
  files?: Record<string, string | Uint8Array>
  directories?: string[]
  modes?: Record<string, number>
  steps: H5HttpStep[]
  faults?: H5HttpFault[]
  environment?: H5ServiceEnvironment
  interfaces?: H5NetworkInterface[]
  activePort?: number
  initialRefresh?: boolean
  randomErrorSize?: number
  trackWritePreparation?: boolean
}
export type H5HttpOutput = {
  results: Array<{ status: number; headers: Record<string, string>; body: string; graceMs: number }>
  trace: Array<{ operation: string; paths: string[] }>
  diagnostics: Array<{ type: string; severity?: string; summary: string }>
  warningCount: number
  randomCalls: number[]
  policy: Array<{ pathname: string; resource?: string; control: boolean; requiresLocalCredential: boolean }>
  writePreparation?: string[]
}

export const h5HttpBase = '/api/h5-access'
export const h5HttpManagedFile = '.claude/cc-haha/settings.json'
const base = h5HttpBase
const file = h5HttpManagedFile
const text = (value: string) => Buffer.from(value).toString('hex')
const request = (method: string, path: string, body?: unknown): H5HttpStep => ({ method, path, ...(body === undefined ? {} : { body: text(JSON.stringify(body)) }) })
const old = '{"env":{},"future":9007199254740993,"h5Access":{"enabled":false,"allowedOrigins":["https://example.com/path"],"disconnectGraceSeconds":42,"futureH5":"dropped by existing normalization"}}'

export const h5HttpScenarios: H5HttpScenario[] = [
  { name: 'missing GET defaults and diagnostics do not create managed state', steps: [request('GET', base)] },
  { name: 'wrong methods, unknown subroutes and explicit segments preserve error bytes', steps: [...['HEAD', 'POST', 'PATCH', 'OPTIONS'].map(method => request(method, base)), ...['enable', 'disable', 'regenerate', 'verify'].map(path => request('GET', base + '/' + path)), request('POST', base + '/unknown'), { ...request('GET', '/ignored'), segments: ['arbitrary', 'resource'] }, { ...request('GET', base), segments: ['api', 'h5-access', ''] }] },
  { name: 'raw control pathname differs from filtered handler segments', files: { [file]: old }, steps: ['/api//h5-access', base + '/', base + '/verify', base + '/verify/', base + '/verify/extra', base + '/%76erify'].map(path => request(path.includes('verify') || path.includes('%76') ? 'POST' : 'GET', path)) },
  { name: 'ignored suffixes and non-JSON POST bodies use real token transitions', steps: [{ ...request('POST', base + '/enable/ignored'), body: text('{bad') }, request('GET', base), { ...request('POST', base + '/verify/extra'), tokenFrom: 0 }, request('POST', base + '/disable/ignored'), { ...request('POST', base + '/verify'), tokenFrom: 0 }, request('POST', base + '/enable'), { ...request('POST', base + '/verify'), tokenFrom: 0 }, request('POST', base + '/regenerate/ignored'), { ...request('POST', base + '/verify'), tokenFrom: 0 }, { ...request('POST', base + '/verify'), tokenFrom: 7 }] },
  { name: 'root PUT rejects null arrays scalar JSON and invalid syntax before settings reads', files: { [file]: old }, steps: [null, [], 1, false, 'text'].map(body => request('PUT', base, body)).concat(['', '{bad', '{} {}', ' \ufeff{}'].map(body => ({ ...request('PUT', base), body: text(body) }))) },
  { name: 'UTF8 replacement leading BOM duplicate keys and unknown body fields preserve source decisions', files: { [file]: old }, steps: [{ ...request('PUT', base), body: text('\ufeff{"fixedPort":28670,"fixedPort":28671,"ignored":true}') }, { ...request('PUT', base), body: Buffer.from([...Buffer.from('{"ignored":"'), 255, ...Buffer.from('"}')]).toString('hex') }, request('PUT', base, { enabled: true, token: 'forged', future: true }), request('GET', base)] },
  { name: 'all four fields retain omitted versus null semantics and refresh real grace cache', files: { [file]: old }, initialRefresh: true, steps: [request('PUT', base, { allowedOrigins: ['https://example.com/path', 'https://example.com/again'], publicBaseUrl: 'https://public.example.com/app/', fixedPort: 28670, disconnectGraceSeconds: 5 }), request('PUT', base, {}), request('PUT', base, { publicBaseUrl: null, fixedPort: null, disconnectGraceSeconds: null }), request('GET', base)] },
  { name: 'invalid port and grace double overflow keep managed bytes and cache unchanged', files: { [file]: old }, initialRefresh: true, steps: [80, 5061, 65536, '28670', 2.5].map(fixedPort => request('PUT', base, { fixedPort })).concat([4, 86401, '10', 1.5].map(disconnectGraceSeconds => request('PUT', base, { disconnectGraceSeconds })), [{ ...request('PUT', base), body: text('{"fixedPort":1e400}') }, { ...request('PUT', base), body: text('{"disconnectGraceSeconds":1e400}') }]) },
  { name: 'origin URL validation uses explicit interfaces without network calls', files: { [file]: old }, interfaces: [{ name: 'en0', entries: [{ family: 'IPv4', internal: false, address: '192.168.1.20' }] }], activePort: 3456, steps: [request('PUT', base, { publicBaseUrl: 'http://192.168.1.20:3456/app' }), request('PUT', base, { publicBaseUrl: 'http://192.168.1.99:3456' }), request('PUT', base, { allowedOrigins: ['https://*.example.com'] }), request('PUT', base, { allowedOrigins: [1] }), request('PUT', base, { publicBaseUrl: 1 }), request('GET', base)] },
  { name: 'verify has its own case-insensitive whitespace parser and uses entire token capture', steps: [request('POST', base + '/enable'), ...['Bearer ', 'bearer ', 'BEARER\t', 'Bearer  '].map(bearerPrefix => ({ ...request('POST', base + '/verify'), tokenFrom: 0, bearerPrefix })), { ...request('POST', base + '/verify'), tokenFrom: 0, bearerPrefix: 'Basic ' }, { ...request('POST', base + '/verify'), headers: [['Authorization', 'Bearer missing']] }, request('POST', base + '/verify?token=ignored')] },
  { name: 'duplicate verification headers are comma joined and rejected', steps: [request('POST', base + '/enable'), { ...request('POST', base + '/verify'), tokenFrom: 0, headers: [['Authorization', 'Bearer extra']] }] },
  { name: 'corrupt managed state is really migrated and protected shared root settings stay byte exact', files: { [file]: '{bad', '.claude/settings.json': '{"protected":"user"}' }, steps: [request('GET', base), request('POST', base + '/enable'), request('GET', base)] },
  { name: 'PUT body abort is an Invalid JSON body API error', steps: [{ ...request('PUT', base), readError: { name: 'AbortError', message: 'fixture cancelled' } }] },
  { name: 'actual managed directory read errno retains source API bytes', directories: [file], steps: [request('GET', base), request('PUT', base, {})] },
  ...(process.platform === 'win32' ? [] : [{ name: 'actual managed permission denied read errno retains source API bytes', files: { [file]: old }, modes: { [file]: 0 }, steps: [request('GET', base), request('PUT', base, {})] }]),
  { name: 'successful PUT persists even when grace refresh read fails and retains prior cached value', files: { [file]: old }, initialRefresh: true, steps: [request('PUT', base, { disconnectGraceSeconds: 8 })], faults: [{ operation: 'read', path: file, message: 'fixture grace read denied', afterManagedCommit: true, once: true }] },
  ...(['read', 'write', 'rename'] as const).map(operation => ({ name: `managed ${operation} failure returns the actual typed API error`, files: { [file]: old }, initialRefresh: true, steps: [request('PUT', base, { disconnectGraceSeconds: 8 })], faults: [{ operation, path: file, message: `fixture ${operation} denied`, once: true }] })),
]
