import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Actual ingress clear consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Pinned owned ingress clear fixture required')
const input = await Bun.stdin.json()
const origin = new URL(input.url).origin
const capabilities = await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: origin })
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const { default: axios } = await import('axios')
axios.defaults.adapter = async config => {
  const url = new URL(axios.getUri(config))
  if (url.origin !== origin || url.username || url.password) throw new Error('Unapproved ingress clear HTTP origin')
  const response = await fetch(url, { method: config.method, headers: config.headers.toJSON() as Record<string, string>, body: config.data })
  return { data: await response.text(), status: response.status, statusText: response.statusText, headers: Object.fromEntries(response.headers), config }
}
Object.assign(process.env, input.env)
const ingress = await import('../../src/services/api/sessionIngress')
const sid = 'actual-clear-ingress'
const initial = await ingress.getSessionLogs(sid, input.url)
const original = ingress.appendSessionLog(sid, { type: 'user', uuid: 'old-admitted', message: { role: 'user', content: 'original admitted transport' } } as any, input.url)
await fetch(origin + '/control/old-started')
ingress.clearAllSessions()
const next = await ingress.appendSessionLog(sid, { type: 'user', uuid: 'new-after-clear', message: { role: 'user', content: 'new independently admitted transport' } } as any, input.url)
await fetch(origin + '/control/release-old')
const before = await original
const last = await ingress.appendSessionLog(sid, { type: 'user', uuid: 'third-after-old-ack', message: { role: 'user', content: 'current global acknowledgement consumer' } } as any, input.url)
if (capabilities.blocked.length) throw new Error('Ingress clear escaped private boundary: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ initial, results: [before, next, last], capabilities }))
process.exit(0)
