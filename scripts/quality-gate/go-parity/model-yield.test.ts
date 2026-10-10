import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { createHTTPSRequestHost, httpsRequestCases, httpsRequestProfile, type HTTPSRequestCase } from './https-request-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/model-yield')
const runArtifact = join(artifact, 'runs', new Date().toISOString().replace(/[:.]/g, '-'))
const certificate = await Bun.file(new URL('./testdata/private-loopback-certificate.pem', import.meta.url)).text()
const key = await Bun.file(new URL('./testdata/private-loopback-key.pem', import.meta.url)).text()
let buildHome: string, executable: string
const observationRuns = new Map<string, number>()
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun required')
  buildHome = await mkdtemp(join(tmpdir(), 'model-yield-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-race', '-coverpkg=./internal/modelyield,./internal/apipresentation,./internal/messages,./internal/requestretry,./internal/provider,./internal/tlsconfig', '-c', '-o', executable, './internal/modelyield'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(item: HTTPSRequestCase, kind: 'source' | 'go' = 'source') {
  const observationKey = item.name + '-' + kind
  const run = (observationRuns.get(observationKey) ?? 0) + 1
  observationRuns.set(observationKey, run)
  const home = await mkdtemp(join(tmpdir(), 'model-yield-home-'))
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
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/https-request.fixture.ts')] : [executable, '-test.run=^TestModelYieldFixture$', '-test.coverprofile=' + join(home, 'native.cover')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_HTTPS_REQUEST_FIXTURE: kind === 'source' ? '1' : '', CC_HAHA_MODEL_YIELD_FIXTURE: kind === 'go' ? '1' : '', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }),
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
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('MODEL_YIELD_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(item.mode === 'abort-before' ? [] : ['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([{ operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed }, { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn }])
        expect(result.sourceOnly.pairingEvidence).toHaveLength(1)
        expect(result.sourceOnly.sdk).toBe('0.80.0')
        if (item.layer === 'creation') expect(result.sourceOnly.queryTerminal.yielded.at(-1)).toContain('isApiErrorMessage')
      }
      if (kind === 'source' && item.layer === 'creation') {
        // This new layer compares the complete actual query terminal. Keep the
        // existing creation-only projection and its raw output intact on disk.
        Object.assign(result.observation, result.sourceOnly.queryTerminal)
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
// The body-cancel timer in the Source fixture is installed only after actual
// SDK response creation; its production host event seam remains separate here.
const cases = httpsRequestCases.filter(item => item.mode !== 'abort-body')
for (const item of cases) test('actual physical model yield Source self + independent Go: ' + item.name, async () => {
  const first = await observe(item)
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item) }])
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item, 'go') }])
}, 65000)

test('model terminal comparator rejects creation, allocation, history and lifecycle drift', async () => {
  const baseline = await observe(cases.find(item => item.name === 'creation-malformed')!)
  const mutants = [
    (value: any) => value.yielded.pop(),
    (value: any) => value.yieldedAfter.pop(),
    (value: any) => value.uuidCalls--,
    (value: any) => value.uuidCallsOnRequest[1]++,
    (value: any) => value.historyAfter[0] += ' ',
    (value: any) => value.outcome = 'creation_failed',
    (value: any) => value.server.closedTCP--,
    (value: any) => value.yielded[value.yielded.length - 1] = value.yielded.at(-1).replace('"unknown"', '"timeout"'),
  ]
  for (const mutate of mutants) {
    const changed = structuredClone(baseline)
    mutate(changed)
    expect(() => assertTraceEqual([{ kind: 'query', value: baseline }], [{ kind: 'query', value: changed }])).toThrow()
  }
}, 30000)
