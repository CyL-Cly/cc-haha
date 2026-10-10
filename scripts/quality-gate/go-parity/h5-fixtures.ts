export type H5Context = {
  clientAddress: string | null
  trustedRendererOrigin?: string | null
  localAccessTokenConfigured?: boolean
  localAccessAuthorized?: boolean
  internalSdkAuthorized?: boolean
}

export type H5RequestFixture = {
  request: { url: string; method: string; headers: Record<string, string[]> }
  context: H5Context
}

export type H5FixtureGroup = {
  name: string
  requests: H5RequestFixture[]
  hosts?: string[]
  rendererOrigins?: Array<string | null>
  paths?: Array<{ pathname: string; context: H5Context }>
}

const local: H5Context = { clientAddress: '127.0.0.1' }
const remote: H5Context = { clientAddress: '192.168.0.44' }
const desktop: H5Context = { ...local, localAccessTokenConfigured: true }
const base = 'http://127.0.0.1:3456'
const capabilities = ['/api/status', '/api/adapters', '/local-file/Users/alice/site/index.html', '/preview-fs/session-1/site/index.html', '/proxy/provider/v1/messages', '/ws/session-1', '/sdk/session-1', '/', '/health', '/assets/app.js']
const origins = ['file://', 'http://localhost:5173', 'http://127.0.1.1:2024', 'http://[::1]:5173', 'https://phone.example', 'http://tauri.localhost', 'https://tauri.localhost', 'tauri://localhost', 'null', '']

function request(path: string, context: H5Context = local, headers: Record<string, string | string[]> = {}, method = 'GET', host = base): H5RequestFixture {
  return { request: { url: host + path, method, headers: Object.fromEntries(Object.entries(headers).map(([name, value]) => [name, typeof value === 'string' ? [value] : value])) }, context }
}

function passive(path: string, referer: string | undefined, destination = 'script', extras: Record<string, string | string[]> = {}, method = 'GET', context = desktop): H5RequestFixture {
  return request(path, context, { 'Sec-Fetch-Site': 'same-origin', 'Sec-Fetch-Dest': destination, ...(referer === undefined ? {} : { Referer: referer.startsWith('/') ? base + referer : referer }), ...extras }, method)
}

export const h5FixtureGroups: H5FixtureGroup[] = [
  { name: 'legacy desktop local origins and token configuration', requests: [
    ...origins.flatMap(origin => [local, desktop, remote].map(context => request('/api/status', context, { Origin: origin }))),
    ...capabilities.flatMap(path => [local, desktop, remote].map(context => request(path, context))),
  ] },
  { name: 'internal SDK precedence and filesystem exclusion', requests: [
    ...[local, remote, { ...remote, internalSdkAuthorized: true }, { ...desktop, internalSdkAuthorized: true }].flatMap(context => ['/sdk/session-1', '/sdk/', '/sdk', '/local-file/secret', '/preview-fs/session/sdk/'].map(path => request(path, context, { Origin: 'https://phone.example', 'X-Forwarded-For': '203.0.113.9' }))),
    request('/sdk/session-1'),
  ] },
  { name: 'LAN capability and bootstrap gates', requests: capabilities.map(path => request(path, remote, { Origin: 'http://192.168.0.20:3456' }, 'GET', 'http://192.168.0.20:3456')) },
  { name: 'process credential precedence over host and browser evidence', requests: capabilities.map(path => request(path, { ...remote, localAccessTokenConfigured: true, localAccessAuthorized: true }, { Origin: 'https://phone.example', Via: '', 'Sec-Fetch-Site': 'cross-site', 'Sec-Fetch-Mode': 'no-cors' }, 'POST', 'https://public.example')) },
  { name: 'all proxy trace headers including empty values', requests: ['Forwarded', 'X-Forwarded-For', 'X-Forwarded-Host', 'X-Forwarded-Proto', 'X-Real-IP', 'Via'].flatMap(header => ['', '203.0.113.9'].flatMap(value => capabilities.map(path => request(path, local, { [header]: value })))) },
  { name: 'cross-site subresources and navigation Fetch Metadata', requests: ['cross-site', 'same-site', 'same-origin', 'none', 'CROSS-SITE'].flatMap(site => [undefined, '', 'navigate', 'cors', 'no-cors'].map(mode => request('/api/sessions', desktop, { 'Sec-Fetch-Site': site, ...(mode === undefined ? {} : { 'Sec-Fetch-Mode': mode }), 'Sec-Fetch-Dest': 'image' }))) },
  { name: 'exact trusted renderer preflight exception', requests: ['OPTIONS', 'options', 'GET', 'HEAD', 'PATCH'].flatMap(method => [undefined, '', 'GET'].flatMap(preflight => ['http://localhost:1420', 'http://localhost:1420/', 'http://localhost:1421'].map(origin => request('/api/h5-access/enable', { ...desktop, trustedRendererOrigin: 'http://localhost:1420' }, { Origin: origin, ...(preflight === undefined ? {} : { 'Access-Control-Request-Method': preflight }) }, method)))) },
  { name: 'passive resource destinations and request methods', requests: ['audio', 'font', 'image', 'manifest', 'script', 'style', 'track', 'video', 'worker', 'empty', 'document', 'Script'].flatMap(destination => ['GET', 'HEAD', 'get', 'POST', 'PATCH'].map(method => passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', destination, { Origin: base }, method))), },
  { name: 'passive referer capability origin and filesystem boundaries', requests: [
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html'),
    passive('/local-file/Users/alice/site/assets/app.js', '/local-file/Users/alice/site/index.html'),
    passive('/preview-fs/session-1/site/assets/app.js', undefined),
    passive('/preview-fs/session-1/site/assets/app.js', 'invalid'),
    passive('/preview-fs/session-1/site/assets/app.js', 'https://attacker.example/site/index.html'),
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', 'script', { Origin: 'invalid' }),
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', 'script', { Origin: 'https://attacker.example' }),
    passive('/preview-fs/session-2/site/assets/app.js', '/preview-fs/session-1/site/index.html'),
    passive('/preview-fs/session-1/site/assets/app.js', '/local-file/Users/alice/site/index.html'),
    passive('/preview-fs/session-1', '/preview-fs/session-1/site/index.html'),
    passive('/preview-fs/session-1/site/app.js', '/preview-fs/session-1'),
    passive('/preview-fs/session-1/secret.js', '/preview-fs/session-1/site/index.html'),
    passive('/local-file/Users/alice/secrets/token.js', '/local-file/Users/alice/site/index.html'),
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', 'script', { Via: '' }, 'GET', remote),
    ...['.css', '.cjs', '.js', '.mjs', '.CSS', '.html', ''].flatMap(extension => [
      passive('/preview-fs/session-1/site/fonts/font.woff', '/preview-fs/session-1/site/assets/dep' + extension),
      passive('/preview-fs/session-1/secrets.js', '/preview-fs/session-1/site/assets/dep' + extension),
      passive('/preview-fs/session-2/font.woff', '/preview-fs/session-1/dep' + extension),
    ]),
    ...['/api/status', '/api/h5-access', '/proxy/provider/v1/messages', '/ws/session-1', '/sdk/session-1'].map(path => passive(path, '/preview-fs/session-1/site/index.html', 'empty', { Origin: base })),
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', 'script', { Origin: 'blob:' + base + '/fixture' }),
    passive('/preview-fs/session-1/site/assets/app.js', '/preview-fs/session-1/site/index.html', 'script', { Origin: 'blob:invalid' }),
  ] },
  { name: 'WHATWG obfuscated hosts encoded paths and dot segments', requests: [
    ...['http://127.1:3456', 'http://2130706433:3456', 'http://0x7f000001:3456', 'http://0177.0.0.1:3456', 'http://127.000.0.1:3456', 'http://%31%32%37.0.0.1:3456', 'http://LOCALHOST:80', 'http://localhost.:3456', 'http://[::1]:3456', 'http://[::ffff:127.0.0.1]:3456', 'https://127.0.1.1:443', 'http://public.example'].map(host => request('/api/status', { clientAddress: '::ffff:127.0.0.1' }, {}, 'GET', host)),
    ...['/preview-fs/session-1/site/%2e%2e/secret.js', '/preview-fs/session-1/site/assets/../app.js', '/preview-fs/session-1/site/assets/%2E/app.js', '/preview-fs/session-1/site/assets%2f../secret.js', '/preview-fs/session-1/site/assets%5c../secret.js', '/api/%2e%2e/health', '/%61pi/status', '/sdk%2fsession-1', '//api/settings/session-cleanup/'].map(path => passive(path, '/preview-fs/session-1/site/index.html')),
    request('/api\\status'),
    request('/api/status?path=/health#fragment'),
    { request: { url: 'not a URL', method: 'GET', headers: {} }, context: local },
  ] },
  { name: 'Fetch header presence empty values and duplicate joins', requests: [
    request('/api/status', desktop, { origin: ['file://', 'https://phone.example'] }),
    request('/api/status', desktop, { origin: '  file://\t' }),
    request('/api/status', desktop, { 'sec-fetch-site': 'cross-site', 'sec-fetch-mode': '' }),
    request('/api/status', desktop, { 'sec-fetch-site': ['cross-site', 'same-origin'], 'sec-fetch-mode': 'no-cors' }),
    passive('/preview-fs/session-1/site/a.js', '/preview-fs/session-1/site/index.html', 'script', { 'Sec-Fetch-Dest': ['script', 'image'] }),
    request('/api/status', local, { via: '' }),
  ] },
  { name: 'host normalization renderer resolution and credential path gates', requests: [],
    hosts: ['localhost', ' LOCALHOST ', '\ufeff[::1]\ufeff', '::1', '[::1]', '127.0.0.1', '127.0.1.1', '127.000.0.001', '127.0.0.256', '127.0.-1.1', '127.bad.0.1', '127.1', '127.example.com', '192.168.0.20', '::ffff:127.0.0.1', '[::ffff:127.0.0.1]', '::ffff:::ffff:127.0.0.1', '::ffff:7f00:1', 'localhost.', '\u0085localhost\u0085', '[[::1]]', 'İΣ'],
    rendererOrigins: [null, '', 'invalid', 'file://', 'ftp://localhost', 'ws://localhost', 'https://remote.example', 'http://localhost:1420/app?x=1#f', 'http://LOCALHOST:80', 'https://127.1:443/', 'http://[::1]:80/path', 'http://[::ffff:127.0.0.1]:1420', 'http://user:pass@localhost:1420', 'http://:@localhost:1420', 'http://user@localhost:1420', 'http://localhost.:1420', 'http://%6cocalhost:1420'],
    paths: ['/api/h5-access', '/api/h5-access/enable', '/api/h5-access-extra', '/api/h5-access/verify', '/api/h5-access/verify/', '//api/h5-access/enable', '/api/settings/session-cleanup', '/api/settings/session-cleanup/', '/api/settings//session-cleanup', '//api/settings/session-cleanup//', '/api/settings/session-cleanup-extra', '/api/settings/user', '/api/settings%2fsession-cleanup', 'api/settings/session-cleanup', ''].flatMap(pathname => [local, desktop, { ...desktop, localAccessAuthorized: true }].map(context => ({ pathname, context }))),
  },
]
