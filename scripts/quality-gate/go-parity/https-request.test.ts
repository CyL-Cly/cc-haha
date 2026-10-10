import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { createHTTPSRequestHost, httpsRequestCases, httpsRequestProfile, type HTTPSRequestCase } from './https-request-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/https-request')
const runArtifact = join(artifact, 'runs', new Date().toISOString().replace(/[:.]/g, '-'))
const certificate = await Bun.file(new URL('./testdata/private-loopback-certificate.pem', import.meta.url)).text()
const key = await Bun.file(new URL('./testdata/private-loopback-key.pem', import.meta.url)).text()
let buildHome: string, executable: string
const observationRuns = new Map<string, number>()
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun required')
  buildHome = await mkdtemp(join(tmpdir(), 'https-request-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-race', '-coverpkg=./internal/requestretry,./internal/provider,./internal/tlsconfig', '-c', '-o', executable, './internal/requestretry'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(item: HTTPSRequestCase, kind: 'source' | 'go' = 'source') {
  const observationKey = item.name + '-' + kind
  const run = (observationRuns.get(observationKey) ?? 0) + 1
  observationRuns.set(observationKey, run)
  const home = await mkdtemp(join(tmpdir(), 'https-request-home-'))
  const host = await createHTTPSRequestHost(item.mode, certificate, key, home)
  try {
    await mkdir(join(home, '.claude'))
    const ca = join(home, 'original-ca.pem')
    if (!['missing', 'default'].includes(item.trust)) await writeFile(ca, item.trust === 'valid' ? certificate : item.trust === 'wrong' ? (await Bun.file(new URL('../../../backend/internal/tlsconfig/bun_1_3_14_roots.json', import.meta.url)).json())[0] : item.trust === 'malformed-large' ? ('x'.repeat(63) + '\n').repeat(16384) : 'private-invalid-pem-input\n')
    const input = { ...httpsRequestProfile, ...item, home, env: {
      ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', API_TIMEOUT_MS: item.apiTimeout ?? (['timeout-handshake', 'late-sse', 'abort-body'].includes(item.mode) ? '1000' : '3000'), CLAUDE_CODE_MAX_RETRIES: '2', CLAUDE_CODE_DISABLE_FAST_MODE: '1',
      ...item.trust !== 'default' ? { NODE_EXTRA_CA_CERTS: ca } : {},
    }, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
    const { trust, apiTimeout, ...original } = input
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/https-request.fixture.ts')] : [executable, '-test.run=^TestHTTPSRequestFixture$', '-test.coverprofile=' + join(home, 'native.cover')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_HTTPS_REQUEST_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }),
      stdin: new Blob([JSON.stringify(original)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      const server = await host.settled()
      await mkdir(artifact, { recursive: true })
      await mkdir(runArtifact, { recursive: true })
      if (kind === 'go' && code === 0) await writeFile(join(artifact, item.name + '-native.cover'), await readFile(join(home, 'native.cover')))
      const rawObservation = JSON.stringify({ original, originalCA: item.trust === 'default' || item.trust === 'missing' ? null : await readFile(ca, 'utf8'), code, stdout, stderr, server, wireRequests: host.wireRequests, tlsMetadata: host.tlsMetadata }, null, 2)
      await writeFile(join(runArtifact, observationKey + '-run-' + run + '-raw.json'), rawObservation)
      await writeFile(join(artifact, observationKey + '-raw.json'), rawObservation)
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('HTTPS_REQUEST_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(item.mode === 'abort-before' ? [] : ['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([{ operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed }, { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn }])
        expect(result.sourceOnly.pairingEvidence).toHaveLength(1)
        expect(result.sourceOnly.sdk).toBe('0.80.0')
        if (item.layer === 'creation') expect(result.sourceOnly.queryTerminal.yielded.at(-1)).toContain('isApiErrorMessage')
      }
      expect(server.closedTCP).toBe(server.acceptedTCP)
      expect(server.closedModelConnections).toBe(server.requests)
      for (const [index, wire] of host.wireRequests.entries()) {
        const request = result.observation.requests[index]
        expect({ path: wire.path, body: wire.body }).toEqual({ path: request.path, body: request.body })
        const received = new Headers(wire.headers)
        for (const [name, value] of request.headers) expect(received.get(name), `${kind} decrypted header ${name}`).toBe(value)
      }
      // Only allocated HOME/origin anchors vary between independent children.
      // Complete literal inputs, failures and wire metadata are retained above.
      const normalized = JSON.parse(JSON.stringify({ ...result.observation, server }).replaceAll(host.origin, '<allocated-loopback-origin>').replaceAll(home, '<allocated-private-home>'))
      const projection = JSON.stringify({ observation: normalized, sourceOnly: result.sourceOnly, wireRequests: host.wireRequests, tlsMetadata: host.tlsMetadata }, null, 2)
      await writeFile(join(runArtifact, observationKey + '-run-' + run + '.json'), projection)
      await writeFile(join(artifact, observationKey + '.json'), projection)
      return normalized
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }) }
}
for (const item of httpsRequestCases) test('actual HTTPS Source self + independent Go: ' + item.name, async () => {
  const first = await observe(item)
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item) }])
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item, 'go') }])
}, 65000)
test('HTTPS physical comparator rejects transport, cause and lifecycle mutations', async () => {
  const baseline = await observe(httpsRequestCases[0])
  const mutants = [
    (value: any) => value.notices[0] = value.notices[0].replace('"connection":"close"', '"connection":"keep-alive"'),
    (value: any) => value.sleeps[1] = 1000,
    (value: any) => value.clients++,
    (value: any) => value.caReads.push({ path: 'original-ca.pem', code: '' }),
    (value: any) => value.server.closedTCP--,
    (value: any) => value.failures[0].json += ' ',
    (value: any) => value.uuidCallsOnRequest[1]++,
    (value: any) => value.requests[1].body += ' ',
  ]
  for (const mutate of mutants) { const changed = structuredClone(baseline); mutate(changed); expect(() => assertTraceEqual([{ kind: 'query', value: baseline }], [{ kind: 'query', value: changed }])).toThrow() }
}, 30000)

for (const kind of ['source', 'go'] as const) for (const fault of ['home', 'config', 'cwd', 'profile', 'origin', 'unknown-env', 'outside-ca', 'symlink-ca', 'prepared', ...kind === 'source' ? ['native', 'network', 'outside-read'] : []]) test(`HTTPS ${kind} pre-business guard: ${fault}`, async () => {
  const home = await mkdtemp(join(tmpdir(), 'https-request-guard-'))
  const outside = await mkdtemp(join(tmpdir(), 'https-request-sentinel-'))
  const sentinel = join(outside, 'protected')
  const host = await createHTTPSRequestHost('sequence', certificate, key)
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(sentinel, 'protected unchanged')
    if (fault === 'symlink-ca') await symlink(sentinel, join(home, 'original-ca.pem'))
    const input = { ...httpsRequestProfile, name: 'guard', mode: 'sequence', layer: 'query', home: fault === 'home' ? outside : home,
      env: { ANTHROPIC_BASE_URL: fault === 'origin' ? 'https://api.deepseek.com:8443' : host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', CLAUDE_CODE_DISABLE_FAST_MODE: '1', ...['outside-ca', 'symlink-ca'].includes(fault) ? { NODE_EXTRA_CA_CERTS: fault === 'outside-ca' ? sentinel : join(home, 'original-ca.pem') } : {}, ...fault === 'unknown-env' ? { CLAUDE_CODE_USE_BEDROCK: '1' } : {} },
      runtime: { platform: process.platform, arch: process.arch, version: process.version },
      ...fault === 'prepared' ? { preparedBody: '{}' } : {}, ...['native', 'network', 'outside-read'].includes(fault) ? { probe: fault, probePath: sentinel } : {},
    }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/https-request.fixture.ts')] : [executable, '-test.run=^TestHTTPSRequestFixture$', '-test.coverprofile=' + join(home, 'native.cover')], {
      cwd: fault === 'cwd' ? outside : home, env: createSandboxedTestEnvironment(home, { CC_HAHA_HTTPS_REQUEST_FIXTURE: fault === 'profile' ? 'denied' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ...fault === 'config' ? { CLAUDE_CONFIG_DIR: outside } : {} }),
      stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 10000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(runArtifact, { recursive: true })
      await writeFile(join(runArtifact, `guard-${kind}-${fault}-raw.json`), JSON.stringify({ input, code, stdout, stderr, server: host.state(), sentinel: await readFile(sentinel, 'utf8') }, null, 2))
      expect(code).not.toBe(0)
      expect(stdout).not.toContain('HTTPS_REQUEST_RESULT:')
      expect(stdout + stderr).toContain(fault === 'prepared' ? kind === 'go' ? 'unknown field' : 'prepared/unknown' : fault === 'unknown-env' ? 'environment' : fault === 'origin' ? 'loopback' : fault === 'outside-ca' ? 'private original CA' : fault === 'symlink-ca' ? 'private CA' : ['native', 'network', 'outside-read'].includes(fault) ? 'blocked' : fault === 'profile' && kind === 'source' ? 'explicit private' : 'disposable HOME/config')
      expect(await readFile(sentinel, 'utf8')).toBe('protected unchanged')
      expect(host.state()).toEqual({ acceptedTCP: 0, closedTCP: 0, requests: 0, closedModelConnections: 0 })
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
}, 15000)
