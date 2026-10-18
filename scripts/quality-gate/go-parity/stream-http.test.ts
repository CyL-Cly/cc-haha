import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { streamHttpHeaderNames, streamHttpScenarios, type StreamHTTPPreparedRequest, type StreamHTTPRequest } from './stream-http-fixtures'
import type { StreamHistoryObservation, StreamHistoryScenario } from './stream-history-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
const observationDir = join(rootDir, 'artifacts/go-parity/stream-http-source')
type Capabilities = { blocked: string[]; secureReads: string[]; absentReads: Array<{ operation: string; path: string }> }
let executable: string

async function record(scenario: StreamHistoryScenario, kind: 'typescript' | 'go' = 'typescript', prepared?: StreamHTTPPreparedRequest) {
  const home = await mkdtemp(join(allocationRoot, 'stream-http-parity-'))
  const requests: StreamHTTPRequest[] = []
  const faults: string[] = []
  const wire = new TextEncoder().encode(scenario.wire)
  // The parent owns the server. Child native/network deny guards stay active,
  // apart from one exact HTTP fetch origin; no Bun.serve exception is granted.
  const server = Bun.serve({
    hostname: '127.0.0.1', port: 0,
    async fetch(request) {
      const url = new URL(request.url)
      const body = Buffer.from(await request.arrayBuffer())
      requests.push({ method: request.method, path: url.pathname + url.search, bodyBytes: body.toString('hex'), headers: Object.fromEntries(streamHttpHeaderNames.map(name => [name, request.headers.get(name)])) })
      if (requests.length !== 1 || request.method !== 'POST' || url.pathname !== '/v1/messages') {
        faults.push('Unexpected fixture request')
        return new Response('Unexpected fixture request', { status: 400 })
      }
      let offset = 0
      return new Response(new ReadableStream<Uint8Array>({
        pull(controller) {
          if (offset === wire.length) return controller.close()
          // Socket/runtime coalescing is allowed. These are server enqueue
          // boundaries, not a claim that HTTP delivers one byte per read.
          controller.enqueue(wire.slice(offset, ++offset))
        },
      }), { headers: { 'content-type': 'text/event-stream', 'request-id': 'fixed-request' } })
    },
  })
  const origin = server.url.origin
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    // A real persisted host snapshot fixes Source getOrCreateUserID without
    // mocking metadata/config/randomness business decisions. It remains private.
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, '.claude', '.claude.json'), JSON.stringify({ userID: 'fixture-http-device' }))
    child = Bun.spawn(kind === 'go' ? [executable, '-test.run=^TestStreamHTTPFixture$'] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/stream-http.fixture.ts')], {
      cwd: home,
      env: createSandboxedTestEnvironment(home, {
        CC_HAHA_STREAM_HISTORY_FIXTURE: '1', CC_HAHA_STREAM_HTTP_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot,
        NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
        CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0',
        ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: origin,
      }),
      stdin: new Blob([JSON.stringify({ homeDir: home, wire: scenario.wire, model: scenario.model, nonInteractive: scenario.nonInteractive, httpOrigin: origin, prepared })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(faults).toEqual([])
      expect(requests).toHaveLength(1)
      let capabilities: Capabilities | undefined
      let source: string | undefined
      if (kind === 'go') source = stdout.split('\n').find(line => line.startsWith('STREAM_HTTP_RESULT:'))?.slice('STREAM_HTTP_RESULT:'.length)
      else {
        const output = JSON.parse(stdout) as { observationSource: string; capabilities: Capabilities }
        source = output.observationSource
        capabilities = output.capabilities
        expect(capabilities.blocked).toEqual([])
        expect(capabilities.secureReads.length).toBeGreaterThan(0)
        expect(capabilities.secureReads.every(call => call === 'read' || call === 'readAsync')).toBeTrue()
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed },
          { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn },
        ])
      }
      expect(source).toBeDefined()
      const observation = JSON.parse(source!) as StreamHistoryObservation
      expect(observation.fetchCount).toBe(requests.length)
      expect(observation.wire).toBe(scenario.wire)
      expect(observation.normalizationError).toBeNull()
      const request = requests[0]!
      // Resolve only beta/helper caller headers into MessagesRequest. Auth,
      // version/content/accept defaults are exercised by Go transport itself.
      const resolved: StreamHTTPPreparedRequest = {
        path: request.path, bodyBytes: request.bodyBytes,
        headers: Object.fromEntries(['anthropic-beta', 'x-stainless-helper'].flatMap(name => request.headers[name] === null ? [] : [[name, request.headers[name]!]])),
      }
      const trace: TraceEvent[] = [
        { kind: 'model-request', value: request },
        { kind: 'state', value: { observation, source: canonicalJsonSource(source!) } },
        { kind: 'stdio', value: { exitCode: code, stderr } },
      ]
      return { observation, request, capabilities, prepared: resolved, trace }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; server.stop(true); await rm(home, { recursive: true, force: true }) }
}

describe('stream HTTP actual Source socket self replay', () => {
  for (const scenario of streamHttpScenarios) test(scenario.name, async () => {
    const first = await record(scenario)
    const second = await record(scenario)
    assertTraceEqual(first.trace, second.trace)
    expect(first.capabilities).toEqual(second.capabilities)
    await mkdir(observationDir, { recursive: true })
    await writeFile(join(observationDir, `${scenario.name}.json`), JSON.stringify({ scenario: scenario.name, profile: 'actual-http-source-query-sdk-default-single-response', trace: first.trace, capabilities: first.capabilities }, null, 2))
  }, 50_000)
})

describe('stream HTTP Go raw transport Controller history parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'stream-http-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'stream-http.exe' : 'stream-http')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/streamhistory'], {
      cwd: join(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of streamHttpScenarios) test(scenario.name, async () => {
    const source = await record(scenario)
    // This is a prepared-request seam: captured Source JSON bytes are handed
    // to Go explicitly. No claim that Go normalized/built the upstream request.
    assertTraceEqual(source.trace, (await record(scenario, 'go', source.prepared)).trace)
  }, 50_000)
})

describe('stream HTTP Source capability boundary before production imports', () => {
  for (const fault of ['missing origin', 'public origin', 'missing HTTP profile', 'HOME mismatch']) test(fault, async () => {
    const home = await mkdtemp(join(allocationRoot, 'stream-http-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/stream-http.fixture.ts')], {
        cwd: home,
        env: createSandboxedTestEnvironment(home, {
          CC_HAHA_STREAM_HISTORY_FIXTURE: '1', CC_HAHA_STREAM_HTTP_FIXTURE: fault === 'missing HTTP profile' ? '' : '1',
          CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1',
        }),
        stdin: new Blob([JSON.stringify({ homeDir: fault === 'HOME mismatch' ? join(home, 'other') : home, wire: '', httpOrigin: fault === 'missing origin' ? undefined : fault === 'public origin' ? 'http://fixture.invalid:43210' : 'http://127.0.0.1:43210' })]),
        stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0)
        expect(stdout).toBe('')
        expect(stderr).toContain(fault === 'public origin' ? 'Fixture fetch capability requires an explicit HTTP(S) loopback origin' : fault === 'HOME mismatch' ? 'Stream history fixture requires disposable HOME/config before production imports' : 'Stream history HTTP requires its explicit fixture profile and origin')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
