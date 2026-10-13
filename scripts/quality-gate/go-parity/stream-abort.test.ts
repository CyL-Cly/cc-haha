import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { requestRetryProfile } from './request-retry-fixtures'
import { createStreamAbortHost, streamAbortCases, type StreamAbortInput } from './stream-abort-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const runArtifact = join(root, 'artifacts/go-parity/stream-abort', new Date().toISOString().replace(/[:.]/g, '-'))
const certificate = await Bun.file(new URL('./testdata/private-loopback-certificate.pem', import.meta.url)).text()
const key = await Bun.file(new URL('./testdata/private-loopback-key.pem', import.meta.url)).text()
let buildHome: string, executable: string
let run = 0
beforeAll(async () => {
  if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Pinned Source runtime required')
  buildHome = await mkdtemp(join(tmpdir(), 'stream-abort-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-race', '-coverpkg=./internal/modelyield,./internal/streamhistory', '-c', '-o', executable, './internal/modelyield'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
  await mkdir(runArtifact, { recursive: true })
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(item: typeof streamAbortCases[number], kind: 'source' | 'go' = 'source', reject?: (input: StreamAbortInput) => void) {
  const home = await mkdtemp(join(tmpdir(), 'stream-abort-home-'))
  const host = await createStreamAbortHost(item.prefix, certificate, key)
  const id = `${item.name}-${kind}-${++run}`
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, 'original-ca.pem'), certificate)
    const input: StreamAbortInput = { ...requestRetryProfile, name: item.name, mode: 'caller-abort', layer: 'query', abortType: item.abortType, abortIndex: item.abortIndex, home, env: { ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-key', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_CODE_DISABLE_FAST_MODE: '1', NODE_EXTRA_CA_CERTS: join(home, 'original-ca.pem') }, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
    reject?.(input)
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/stream-abort.fixture.ts')] : [executable, '-test.run=^TestStreamAbortFixture$', '-test.coverprofile=' + join(home, 'native.cover')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_STREAM_ABORT_FIXTURE: '1', CC_HAHA_MODEL_YIELD_FIXTURE: kind === 'go' ? '1' : '', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      const server = await host.settled()
      await writeFile(join(runArtifact, id + '-raw.json'), JSON.stringify({ input, prefix: item.prefix, code, stdout, stderr, server, wireRequests: host.wireRequests }, null, 2))
      if (reject) {
        expect(code).not.toBe(0)
        expect(stderr + stdout).toMatch(/requires|reject|unknown field|before imports|disposable/i)
        expect(server).toEqual({ acceptedTCP: 0, closedTCP: 0, requests: 0, closedModelConnections: 0 })
        return { rejected: true }
      }
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('STREAM_ABORT_RESULT:')[1].split('\n')[0])
      if (kind === 'source') { expect(result.sourceOnly.capabilities.blocked).toEqual([]); expect(result.sourceOnly.capabilities.secureReads).toEqual(['read']); expect(result.sourceOnly.sdk).toBe('0.128.0') }
      expect(server.requests).toBe(1)
      expect(server.closedModelConnections).toBe(1)
      expect(server.closedTCP).toBe(server.acceptedTCP)
      expect(host.wireRequests).toHaveLength(1)
      const wire = host.wireRequests[0], request = result.observation.requests[0]
      expect({ path: wire.path, body: wire.body }).toEqual({ path: request.path, body: request.body })
      const received = new Headers(wire.headers)
      for (const [name, value] of request.headers) expect(received.get(name), `${kind} actual decrypted header ${name}`).toBe(value)
      if (kind === 'go') await writeFile(join(runArtifact, id + '.cover'), await readFile(join(home, 'native.cover')))
      const normalized = JSON.parse(JSON.stringify({ observation: result.observation, server }).replaceAll(host.origin, '<allocated-loopback-origin>').replaceAll(home, '<allocated-private-home>'))
      await writeFile(join(runArtifact, id + '-projection.json'), JSON.stringify(normalized, null, 2))
      return normalized
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }) }
}
for (const item of streamAbortCases) test('actual held HTTPS cancellation Source self + independent Go: ' + item.name, async () => {
  const source = await observe(item)
  assertTraceEqual([{ kind: 'model', value: source }], [{ kind: 'model', value: await observe(item) }])
  assertTraceEqual([{ kind: 'model', value: source }], [{ kind: 'model', value: await observe(item, 'go') }])
}, 65000)
test('partial commit comparator rejects visible text, pending tools, allocation and lifecycle drift', async () => {
  const baseline = await observe(streamAbortCases[2])
  for (const mutate of [
    (value: any) => value.observation.yielded.pop(),
    (value: any) => value.observation.yieldedAfter.pop(),
    (value: any) => value.observation.uuidCalls++,
    (value: any) => value.observation.dateCalls++,
    (value: any) => value.observation.preparationCalls++,
    (value: any) => value.observation.aborted = false,
    (value: any) => value.observation.requests[0].body += ' ',
    (value: any) => value.observation.historyAfter[0] += ' ',
    (value: any) => value.server.closedModelConnections--,
  ]) { const changed = structuredClone(baseline); mutate(changed); expect(() => assertTraceEqual([{ kind: 'model', value: baseline }], [{ kind: 'model', value: changed }])).toThrow() }
})

for (const [name, mutate] of [
  ['prepared shortcut', (input: any) => input.preparedRequest = '{}'],
  ['public origin', (input: StreamAbortInput) => input.env.ANTHROPIC_BASE_URL = 'https://example.com/'],
  ['outside CA', (input: StreamAbortInput) => input.env.NODE_EXTRA_CA_CERTS = '/outside-original.pem'],
  ['HOME mismatch', (input: StreamAbortInput) => input.home += '-mismatch'],
  ['unknown control', (input: StreamAbortInput) => input.abortIndex = 42],
  ['missing profile', (input: StreamAbortInput) => input.mode = '' as StreamAbortInput['mode']],
] as const) for (const kind of ['source', 'go'] as const) test('stream cancellation boundary before business: ' + kind + ' ' + name, async () => {
  await observe(streamAbortCases[0], kind, mutate)
})
