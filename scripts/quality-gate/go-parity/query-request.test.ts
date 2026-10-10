import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { queryRequestCases, type QueryRequestCase, type QueryRequestInput, type QueryRequestObservation } from './query-request-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'
const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let executable: string
const identity = { runtime: { platform: process.platform, arch: process.arch, version: process.version }, deviceID: 'fixture-query-device', sessionID: '00000000-0000-4000-8000-000000000000' }
async function record(scenario: QueryRequestCase, kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(allocationRoot, 'query-request-parity-'))
  const wireRequests: Array<{ path: string; method: string; body: string; headers: Array<[string, string]> }> = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    const url = new URL(request.url)
    wireRequests.push({ path: url.pathname + url.search, method: request.method, body: await request.text(), headers: [...request.headers] })
    if (request.method !== 'POST' || url.pathname !== '/v1/messages' || wireRequests.length !== 1) return new Response('Unexpected fixture request', { status: 400 })
    return new Response(scenario.wire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'query-request-response' } })
  } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, '.claude', '.claude.json'), JSON.stringify({ userID: identity.deviceID }))
    const input: QueryRequestInput = { ...scenario, ...identity, home, origin: server.url.origin }
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/query-request.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_QUERY_REQUEST_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: '0', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin }),
      stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      const output = JSON.parse(stdout)
      if (kind === 'typescript') {
        expect(output.sourceOnly.capabilities.blocked).toEqual([])
        expect(output.sourceOnly.capabilities.secureReads).toEqual(['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(output.sourceOnly.capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed },
          { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn }, { operation: 'readdirSync', path: dropIn },
        ])
      }
      const source = output.observationSource as string
      const observation = JSON.parse(source) as QueryRequestObservation
      expect(observation.error).toBeNull()
      expect(observation.terminal).toEqual({ reason: 'completed' })
      expect(wireRequests).toHaveLength(1)
      expect(observation.requests).toHaveLength(1)
      // Assert every explicitly resolved pre-fetch header reaches the socket;
      // automatic runtime HTTP headers remain side-specific metadata.
      for (const [index, request] of observation.requests.entries()) {
        expect({ path: wireRequests[index]!.path, method: wireRequests[index]!.method, body: wireRequests[index]!.body }).toEqual({ path: request.path, method: request.method, body: request.body })
        const received = new Headers(wireRequests[index]!.headers)
        for (const [name, value] of request.headers) expect(received.get(name), name).toBe(value)
      }
      const trace: TraceEvent[] = [{ kind: 'state', value: { observation, source: canonicalJsonSource(source) } }, { kind: 'model-request', value: wireRequests.map(({ path, method, body }) => ({ path, method, body })) }, { kind: 'stdio', value: { code, stderr } }]
      return { trace, observation, sourceOnly: { ...output.sourceOnly, wireRequests } }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; server.stop(true); await rm(home, { recursive: true, force: true }) }
}

describe('actual query independent original-request Source self replay', () => {
  for (const scenario of queryRequestCases) test(scenario.name, async () => {
    const source = await record(scenario)
    assertTraceEqual(source.trace, (await record(scenario)).trace)
    expect(source.observation.modelHistory).toHaveLength(1)
    expect(source.observation.history).toHaveLength(scenario.historySources.length + 1)
    await mkdir(join(root, 'artifacts/go-parity/query-request-source'), { recursive: true })
    await writeFile(join(root, 'artifacts/go-parity/query-request-source', `${scenario.name}.json`), JSON.stringify({ scenario, ...identity, ...source }, null, 2))
  }, 50_000)
  test('strict trace detects request, header, history, yield and allocation drift', async () => {
    const source = await record(queryRequestCases[0]!)
    const mutations = [
      (value: QueryRequestObservation) => { value.requests[0]!.body += '\n' },
      (value: QueryRequestObservation) => { value.requests[0]!.path = '/v1/messages' },
      (value: QueryRequestObservation) => { value.requests[0]!.headers.find(([name]) => name === 'x-api-key')![1] = 'wrong-fixture-key' },
      (value: QueryRequestObservation) => { value.history.reverse() },
      (value: QueryRequestObservation) => { value.yieldedAfter[value.yieldedAfter.length - 1] += ' ' },
      (value: QueryRequestObservation) => { value.allocations.reverse() },
    ]
    for (const mutate of mutations) {
      const changed = structuredClone(source.observation)
      mutate(changed)
      const trace = structuredClone(source.trace)
      trace[0] = { kind: 'state', value: { observation: changed, source: canonicalJsonSource(JSON.stringify(changed)) } }
      expect(() => assertTraceEqual(source.trace, trace)).toThrow()
    }
  }, 30_000)
})

describe('actual query independent Go request Build HTTP Controller loop parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'query-request-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'query-request.exe' : 'query-request')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/queryloop/testdata/request-fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of queryRequestCases) test(scenario.name, async () => {
    const source = await record(scenario)
    // Only original scenario, host identity and fault wire reach Go. There is
    // no Source-derived prepared/body/path/schema/options shortcut.
    assertTraceEqual(source.trace, (await record(scenario, 'go')).trace)
  }, 50_000)
  for (const fault of ['missing origin', 'public origin', 'missing profile', 'HOME mismatch', 'prepared shortcut']) test(`Go rejects ${fault} before request construction`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'query-request-go-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const origin = fault === 'missing origin' ? undefined : fault === 'public origin' ? 'http://fixture.invalid:43210' : 'http://127.0.0.1:43210'
      const sentinel = join(home, 'sentinel.json')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      child = Bun.spawn([executable], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_QUERY_REQUEST_FIXTURE: fault === 'missing profile' ? '' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', ...(origin ? { ANTHROPIC_BASE_URL: origin } : {}) }),
        stdin: new Blob([JSON.stringify({ ...queryRequestCases[0], ...identity, home: fault === 'HOME mismatch' ? join(home, 'other') : home, origin, ...(fault === 'prepared shortcut' ? { prepared: { body: '{}' } } : {}) })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0); expect(stdout).toBe('')
        expect(stderr).toContain(fault === 'prepared shortcut' ? 'unknown field "prepared"' : fault === 'missing profile' ? 'explicit tool-free offline profile' : fault === 'HOME mismatch' ? 'disposable HOME/config before business execution' : 'exact HTTP loopback origin')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})

describe('query original-request fixture guard before production imports', () => {
  for (const fault of ['missing origin', 'public origin', 'missing profile', 'HOME mismatch', 'prepared shortcut']) test(fault, async () => {
    const home = await mkdtemp(join(allocationRoot, 'query-request-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const origin = fault === 'missing origin' ? undefined : fault === 'public origin' ? 'http://fixture.invalid:43210' : 'http://127.0.0.1:43210'
      const fixture = join(root, 'scripts/quality-gate/go-parity/query-request.fixture.ts')
      const loader = join(home, 'production-tripwire.ts')
      const sentinel = join(home, 'sentinel.json')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'query-request-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(fixture)})`)
      child = Bun.spawn([process.execPath, '--no-env-file', loader], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_QUERY_REQUEST_FIXTURE: fault === 'missing profile' ? '' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', ...(origin ? { ANTHROPIC_BASE_URL: origin } : {}) }),
        stdin: new Blob([JSON.stringify({ ...queryRequestCases[0], ...identity, home: fault === 'HOME mismatch' ? join(home, 'other') : home, origin, ...(fault === 'prepared shortcut' ? { prepared: { body: '{}' } } : {}) })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0); expect(stdout).toBe('')
        expect(stderr).toContain(fault === 'public origin' ? 'Fixture fetch capability requires an explicit HTTP(S) loopback origin' : fault === 'HOME mismatch' ? 'Query request fixture requires disposable HOME/config before production imports' : 'Query request requires explicit tool-free offline profile and original inputs')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
