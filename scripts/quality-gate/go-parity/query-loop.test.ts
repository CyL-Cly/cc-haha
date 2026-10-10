import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { queryLoopScenarios, type QueryLoopObservation, type QueryLoopScenario } from './query-loop-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'
const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let executable: string
const identity = { runtime: { platform: process.platform, arch: process.arch, version: process.version }, deviceID: 'fixture-query-device', sessionID: '00000000-0000-4000-8000-000000000000' }
async function record(scenario: QueryLoopScenario, kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(allocationRoot, 'query-loop-parity-'))
  const requests: Array<{ path: string; method: string; body: string; headers: Array<[string, string]> }> = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    const url = new URL(request.url)
    requests.push({ path: url.pathname + url.search, method: request.method, body: await request.text(), headers: [...request.headers] })
    if (request.method !== 'POST' || url.pathname !== '/v1/messages' || requests.length > scenario.wires.length) return new Response('Unexpected fixture request', { status: 400 })
    return new Response(scenario.wires[requests.length - 1], { headers: { 'content-type': 'text/event-stream', 'request-id': `fixture-request-${requests.length}` } })
  } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, '.claude', '.claude.json'), JSON.stringify({ userID: 'fixture-query-device' }))
    child = Bun.spawn(kind === 'go' ? [executable, '-test.run=^TestQueryLoopFixture$'] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/query-loop.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_QUERY_LOOP_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: '0', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin }),
      stdin: new Blob([JSON.stringify({ ...scenario, ...identity, home, origin: server.url.origin })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stdout + stderr).toBe(0)
      const output = kind === 'typescript' ? JSON.parse(stdout) : JSON.parse(stdout.split('\n').find(line => line.startsWith('QUERY_LOOP_RESULT:'))!.slice('QUERY_LOOP_RESULT:'.length))
      if (kind === 'typescript') {
        expect(output.capabilities.blocked).toEqual([])
        expect(output.capabilities.secureReads).toEqual(['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(output.capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed },
          { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn },
          { operation: 'readdirSync', path: dropIn },
        ])
      }
      const source = output.observationSource as string
      const observation = JSON.parse(source) as QueryLoopObservation
      expect(requests).toHaveLength(2)
      expect(observation.requests).toHaveLength(2)
      for (const [index, request] of observation.requests.entries()) {
        expect({ path: requests[index]!.path, method: requests[index]!.method, body: requests[index]!.body }).toEqual({ path: request.path, method: request.method, body: request.body })
        const received = new Headers(requests[index]!.headers)
        for (const [name, value] of request.headers) expect(received.get(name), name).toBe(value)
      }
      const trace: TraceEvent[] = [{ kind: 'state', value: { observation, source: canonicalJsonSource(source) } }, { kind: 'model-request', value: requests.map(({ path, method, body }) => ({ path, method, body })) }, { kind: 'stdio', value: { code, stderr } }]
      return { trace, requests: observation.requests, observation, capabilities: output.capabilities, sourceOnly: { runtime: identity.runtime, wireRequests: requests } }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; server.stop(true); await rm(home, { recursive: true, force: true }) }
}
describe('query loop actual production Source self replay', () => {
  for (const scenario of queryLoopScenarios) test(scenario.name, async () => {
    const first = await record(scenario), second = await record(scenario)
    assertTraceEqual(first.trace, second.trace)
    expect(first.capabilities).toEqual(second.capabilities)
    expect(first.observation.error).toBeNull()
    expect(first.observation.terminal).toEqual({ reason: 'completed' })
    if (scenario.identityTrace) {
      const ids=first.observation.effects.filter(effect=>effect.kind==='context-ID').map(effect=>effect.value)
      expect(first.observation.executions).toBe(ids.length)
      expect(first.observation.effects.filter(effect=>effect.kind==='mapped-ID').map(effect=>effect.value)).toEqual(ids)
      expect(first.observation.file).toBe(ids.length ? JSON.stringify(ids.map(raw=>JSON.parse(raw))) : null)
      if (scenario.name.includes('distinct-lone-ID')) expect(ids).toEqual([JSON.stringify('\ud800'),JSON.stringify('�'),JSON.stringify('\udfff')])
      if (scenario.name.includes('unknown-high-name-then-replacement')) expect(ids).toEqual([JSON.stringify('\ud800')])
      if (scenario.name.includes('unknown-low-name-then-alias')) expect(ids).toEqual([JSON.stringify('\udfff')])
      if (scenario.name.includes('primitive') || scenario.name.includes('marker')) expect(ids).toEqual([])
    } else expect(first.observation.executions).toBe(1)
    expect(first.observation.effects.filter(effect => effect.kind === 'prompt')).toEqual([{ kind: 'prompt', value: JSON.stringify(scenario.definition.prompt) }])
    if (!scenario.identityTrace) expect(first.observation.file).toBe(scenario.permission === 'replace-content' ? 'permission content' : 'written exactly once')
    expect(first.requests).toHaveLength(2)
    expect(first.observation.modelHistory).toHaveLength(2)
    expect(first.observation.modelHistory[1]!.join('\n')).not.toContain('legacy_view')
    expect(first.observation.modelHistory[1]!.join('\n')).not.toContain('effect-overwritten')
    expect(JSON.parse(first.requests[0]!.body).tools[0].name).toBe(scenario.definition.name)
    expect(JSON.parse(first.requests[0]!.body).tools[0].input_schema.properties).toEqual({ file_path: { type: 'string' }, content: { type: 'string' } })
    if (scenario.name.includes('strip-only-execution')) {
      expect(first.observation.effects.find(effect => effect.kind === 'tool')!.value).toBe('{"file_path":"effect.txt","content":"written exactly once"}')
      expect(first.requests[1]!.body).toContain('kept in model history')
      expect(first.requests[1]!.body).toContain('\\ud800')
    }
    if (scenario.name.includes('empty-result')) expect(first.requests[1]!.body).toContain('(FixtureWrite completed with no output)')
    if (scenario.name.includes('strip-result-references')) {
      expect(first.observation.yielded.join('\n')).toContain('tool_reference')
      expect(first.observation.modelHistory[1]!.join('\n')).not.toContain('tool_reference')
      expect(first.requests[1]!.body).not.toContain('tool_reference')
    }
    if (scenario.backfill === 'add' && first.observation.executions) expect(first.observation.yielded.join('\n')).toContain('legacy_view')
    if (scenario.backfill === 'overwrite') expect(first.observation.yielded.join('\n')).not.toContain('effect-overwritten')
    await mkdir(join(root, 'artifacts/go-parity/query-loop-source'), { recursive: true })
    await writeFile(join(root, 'artifacts/go-parity/query-loop-source', `${scenario.name}.json`), JSON.stringify({ scenario, ...identity, ...first }, null, 2))
  }, 50_000)
  test('strict comparator rejects tool schema, second request, backfill, permission and side-effect drift', async () => {
    const source = await record(queryLoopScenarios[0]!)
    const mutations = [
      (value: QueryLoopObservation) => { value.requests[0]!.body = value.requests[0]!.body.replace('"name":"FixtureWrite"', '"name":"WrongNativeTool"') },
      (value: QueryLoopObservation) => { value.requests[1]!.body += '\n' },
      (value: QueryLoopObservation) => { value.requests[1]!.path = '/v1/messages?beta=wrong' },
      (value: QueryLoopObservation) => { value.requests[0]!.headers.find(([name]) => name === 'x-api-key')![1] = 'wrong-fixture-key' },
      (value: QueryLoopObservation) => { value.modelHistory[1]!.reverse() },
      (value: QueryLoopObservation) => { value.history[1] += 'legacy_view' },
      (value: QueryLoopObservation) => { value.yielded[1] += '\n' },
      (value: QueryLoopObservation) => { value.effects.reverse() },
      (value: QueryLoopObservation) => { value.allocations.reverse() },
      (value: QueryLoopObservation) => { value.executions++ },
      (value: QueryLoopObservation) => { value.file = 'wrong content' },
    ]
    for (const mutate of mutations) {
      const observation = structuredClone(source.observation)
      mutate(observation)
      const trace = structuredClone(source.trace)
      trace[0] = { kind: 'state', value: { observation, source: canonicalJsonSource(JSON.stringify(observation)) } }
      expect(() => assertTraceEqual(source.trace, trace)).toThrow()
    }
  }, 30_000)
})

describe('raw execution identity mutation sensitivity', () => {
  test('rejects lossy tool IDs, missing unknown feedback and altered next-request history', async () => {
    const source = await record(queryLoopScenarios.find(scenario => scenario.name === 'headless-identity-distinct-lone-ID-repeat')!)
    const mutations = [
      (value: QueryLoopObservation) => { value.effects.find(effect => effect.kind === 'context-ID')!.value = JSON.stringify('�') },
      (value: QueryLoopObservation) => { value.effects.find(effect => effect.kind === 'mapped-ID')!.value = JSON.stringify('�') },
      (value: QueryLoopObservation) => { value.effects.find(effect => effect.kind === 'in-progress')!.value = '["�"]' },
      (value: QueryLoopObservation) => { value.requests[1]!.body = value.requests[1]!.body.replace('\\ud800', '�') },
      (value: QueryLoopObservation) => { value.history = value.history.filter(raw => !raw.includes('tool_result')) },
      (value: QueryLoopObservation) => { value.executions = 4 },
      (value: QueryLoopObservation) => { value.file = '["�","�","�"]' },
    ]
    for (const mutate of mutations) {
      const observation = structuredClone(source.observation)
      mutate(observation)
      const trace = structuredClone(source.trace)
      trace[0] = { kind: 'state', value: { observation, source: canonicalJsonSource(JSON.stringify(observation)) } }
      expect(() => assertTraceEqual(source.trace, trace)).toThrow()
    }
    const unknown = await record(queryLoopScenarios.find(scenario => scenario.name === 'headless-identity-unknown-high-name-then-replacement')!)
    const trace = structuredClone(unknown.trace)
    const observation = structuredClone(unknown.observation)
    observation.yielded = observation.yielded.filter(raw => !raw.includes('No such tool available'))
    trace[0] = { kind: 'state', value: { observation, source: canonicalJsonSource(JSON.stringify(observation)) } }
    expect(() => assertTraceEqual(unknown.trace, trace)).toThrow()
  }, 30_000)
})

describe('query loop Source capability boundary before production imports', () => {
  for (const fault of ['missing origin', 'public origin', 'missing profile', 'HOME mismatch', 'prepared shortcut']) test(fault, async () => {
    const home = await mkdtemp(join(allocationRoot, 'query-loop-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const origin = fault === 'missing origin' ? undefined : fault === 'public origin' ? 'http://fixture.invalid:43210' : 'http://127.0.0.1:43210'
      const fixture = join(root, 'scripts/quality-gate/go-parity/query-loop.fixture.ts')
      const loader = join(home, 'production-tripwire.ts')
      const sentinel = join(home, 'sentinel.json')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'query-loop-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(fixture)})`)
      child = Bun.spawn([process.execPath, '--no-env-file', loader], {
        cwd: home, env: createSandboxedTestEnvironment(home, {
          CC_HAHA_QUERY_LOOP_FIXTURE: fault === 'missing profile' ? '' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production',
          ...(origin ? { ANTHROPIC_BASE_URL: origin } : {}),
        }),
        stdin: new Blob([JSON.stringify({ ...queryLoopScenarios[0], ...identity, home: fault === 'HOME mismatch' ? join(home, 'other') : home, origin, ...(fault === 'prepared shortcut' ? { prepared: [{ body: '{}' }] } : {}) })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0)
        expect(stdout).toBe('')
        expect(stderr).toContain(fault === 'public origin' ? 'Fixture fetch capability requires an explicit HTTP(S) loopback origin' : fault === 'HOME mismatch' ? 'Query loop fixture requires disposable HOME/config before production imports' : 'Query loop requires explicit offline production profile')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
describe('query loop independently built native-tool HTTP Controller Go parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'query-loop-build-'))
    executable = join(buildHome, 'query-loop')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/queryloop'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  }, 180_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of queryLoopScenarios) test(scenario.name, async () => {
    const source = await record(scenario)
    assertTraceEqual(source.trace, (await record(scenario, 'go')).trace)
  }, 50_000)
  for (const fault of ['missing origin', 'public origin', 'missing profile', 'HOME mismatch', 'prepared shortcut']) test(`Go rejects ${fault} before construction or tool execution`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'query-loop-go-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      await mkdir(join(home, '.claude'))
      const sentinel = join(home, 'sentinel.json')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      const origin = fault === 'missing origin' ? undefined : fault === 'public origin' ? 'http://fixture.invalid:43210' : 'http://127.0.0.1:43210'
      child = Bun.spawn([executable, '-test.run=^TestQueryLoopFixture$'], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_QUERY_LOOP_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: fault === 'missing profile' ? '' : 'production', ...(origin ? { ANTHROPIC_BASE_URL: origin } : {}) }),
        stdin: new Blob([JSON.stringify({ ...queryLoopScenarios[0], ...identity, home: fault === 'HOME mismatch' ? join(home, 'other') : home, origin, ...(fault === 'prepared shortcut' ? { prepared: [{ body: '{}' }] } : {}) })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0)
        expect(stdout).not.toContain('QUERY_LOOP_RESULT:')
        expect(stdout + stderr).toContain(fault === 'prepared shortcut' ? 'unknown field "prepared"' : fault === 'HOME mismatch' ? 'disposable query loop HOME/config' : fault === 'missing profile' ? 'explicit query loop production fixture' : 'exact HTTP loopback origin')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
