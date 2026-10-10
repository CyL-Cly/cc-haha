import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { createRequestRetryHost } from './request-retry-host'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { sharedOpenerCases, sharedOpenerMediaCases, type SharedOpenerCase } from './shared-opener-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifacts = join(root, 'artifacts/go-parity/shared-opener')
let buildHome: string
let executable: string
const captured: unknown[] = []
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'shared-opener-build-'))
  executable = join(buildHome, 'requestretry.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/requestretry'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
  if (process.env.CC_HAHA_CAPTURE_SHARED_OPENER === '1') {
    expect(captured).toHaveLength(sharedOpenerCases.length)
    await writeFile(join(root, 'backend/internal/requestretry/testdata/shared_opener_contract.json'), JSON.stringify({ bun: Bun.version, revision: Bun.revision, source: 'actual queryModelWithStreaming original bare/native/extra/tool_result profiles', cases: captured }, null, 2) + '\n')
  }
})
async function observe(item: SharedOpenerCase, kind: 'source' | 'go', replay: number) {
  const home = await mkdtemp(join(tmpdir(), 'shared-opener-source-'))
  const host = createRequestRetryHost(item.mode)
  try {
    await mkdir(join(home, '.claude'))
    const input = { ...item, home, env: { ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', API_TIMEOUT_MS: '3000', CLAUDE_CODE_MAX_RETRIES: '2', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_SIMPLE: '1' }, deviceID: item.deviceID, sessionID: item.sessionID, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/request-retry.fixture.ts')] : [executable, '-test.run=^TestRequestRetryFixture$'], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_REQUEST_RETRY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      const server = await host.settled()
      let result: any
      if (code === 0) result = JSON.parse(kind === 'source' ? stdout : stdout.split('REQUEST_RETRY_RESULT:')[1].split('\n')[0])
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, `${item.name.replace(/\W+/g, '-')}-${kind}-${replay}.json`), JSON.stringify({ input, code, stdout, stderr, server, wireRequests: host.wireRequests, result }, null, 2))
      expect(code, stdout + stderr).toBe(0)
      expect(stderr).toBe('')
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(item.mode === 'abort-before' ? [] : ['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([{ operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed }, { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn }])
        expect(result.sourceOnly.pairingEvidence).toHaveLength(1)
      }
      const observation = { ...result.observation, server }
      expect(observation.preparationCalls).toBe(1)
      expect(server.closedModelConnections).toBe(server.requests)
      expect(observation.requests).toHaveLength(host.wireRequests.length)
      for (const [index, request] of observation.requests.entries()) {
        const wire = host.wireRequests[index]
        expect({ path: wire.path, body: wire.body }).toEqual({ path: request.path, body: request.body })
        for (const [name, value] of request.headers) expect(new Headers(wire.headers).get(name)).toBe(value)
      }
      return { input: { ...input, home: '<private-home>', env: { ...input.env, ANTHROPIC_BASE_URL: '<per-child-loopback>' } }, observation }
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }) }
}
for (const item of sharedOpenerCases) test(`actual Source self + independent shared opener: ${item.name}`, async () => {
  const first = await observe(item, 'source', 1)
  const second = await observe(item, 'source', 2)
  assertTraceEqual([{ kind: 'query', value: first.observation }], [{ kind: 'query', value: second.observation }])
  captured.push({ input: first.input, expected: first.observation })
  const native = await observe(item, 'go', 1)
  assertTraceEqual([{ kind: 'query', value: first.observation }], [{ kind: 'query', value: native.observation }])
}, 65000)

for (const item of sharedOpenerMediaCases) test(`actual Source self + independent processed media opener: ${item.name}`, async () => {
  const first = await observe(item, 'source', 1)
  const second = await observe(item, 'source', 2)
  const native = await observe(item, 'go', 1)
  assertTraceEqual([{ kind: 'query', value: first.observation }], [{ kind: 'query', value: second.observation }])
  assertTraceEqual([{ kind: 'query', value: first.observation }], [{ kind: 'query', value: native.observation }])
  for (const observation of [first.observation, second.observation, native.observation]) {
    expect(observation.outcome).toBe('success')
    expect(observation.requests).toHaveLength(3)
    for (const request of observation.requests) {
      const messages = JSON.parse(request.body).messages
      if (item.name.startsWith('original orphan')) {
        expect(messages).toEqual([{ role: 'user', content: [{ type: 'text', text: '[Orphaned tool result removed due to conversation resume]', cache_control: { type: 'ephemeral' } }] }])
      } else {
        const result = messages.flatMap((message: any) => message.content).find((block: any) => block.type === 'tool_result')
        const media = result.content.find((block: any) => block.type === (item.name.includes('image') ? 'image' : 'document'))
        expect(media.source).toEqual(item.name.includes('image')
          ? { type: 'base64', media_type: 'image/png', data: 'AA==' }
          : { type: 'base64', media_type: 'application/pdf', data: 'JVBERg==' })
      }
    }
  }
}, 65000)

test('strict original-input comparator detects preparation/auth/schema/history changes', async () => {
  const first = await observe(sharedOpenerCases[3], 'source', 3)
  for (const mutate of [
    (value: any) => value.preparationCalls++,
    (value: any) => value.requests[0].headers.find(([name]: string[]) => name === 'x-api-key')[1] = 'prepared-credential',
    (value: any) => value.requests[1].body += ' ',
    (value: any) => value.normalizedHistory[0].messageSource += ' ',
    (value: any) => value.promptCalls.FixtureWrite++,
    (value: any) => value.uuidCallsOnRequest[1]++,
  ]) {
    const altered = structuredClone(first.observation)
    mutate(altered)
    expect(() => assertTraceEqual([{ kind: 'query', value: first.observation }], [{ kind: 'query', value: altered }])).toThrow()
  }
})

for (const kind of ['source', 'go'] as const) for (const fault of ['unknown environment', 'invalid bare value', 'non-bare interactive', 'original flag path', 'original flag inline', 'unsupported tool result media', 'prepared shortcut']) test(`${kind} original opener rejects unsupported input before effects: ${fault}`, async () => {
  const home = await mkdtemp(join(tmpdir(), 'shared-opener-guard-'))
  const outside = await mkdtemp(join(tmpdir(), 'shared-opener-sentinel-'))
  const sentinel = join(outside, 'sentinel.json')
  const host = createRequestRetryHost('sequence')
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(sentinel, '{"protected":"unchanged"}')
    const env: Record<string, string> = { ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_SIMPLE: '1' }
    if (fault === 'unknown environment') env.UNKNOWN_OPENER_ENV = '1'
    if (fault === 'invalid bare value') env.CLAUDE_CODE_SIMPLE = 'true'
    if (fault === 'non-bare interactive') delete env.CLAUDE_CODE_SIMPLE
    const input = { ...sharedOpenerCases[0], home, env, runtime: { platform: process.platform, arch: process.arch, version: process.version },
      ...fault === 'original flag path' ? { auth: { flagSettingsPath: sentinel } } : {},
      ...fault === 'original flag inline' ? { auth: { flagSettingsInline: { apiKeyHelper: 'must never spawn' } } } : {},
      ...fault === 'unsupported tool result media' ? { historySources: ['{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"audio","source":{"type":"base64","data":"AA=="}}]}]}}'] } : {},
      ...fault === 'prepared shortcut' ? { preparedBody: '{}' } : {},
    }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/request-retry.fixture.ts')] : [executable, '-test.run=^TestRequestRetryFixture$'], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_REQUEST_RETRY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 10000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout + stderr).toContain(fault === 'unknown environment' ? 'environment' : fault === 'invalid bare value' || fault === 'non-bare interactive' ? kind === 'source' ? 'bare/interactive' : fault === 'invalid bare value' ? 'bare environment value' : 'non-bare interactive' : fault.startsWith('original flag') ? 'flag settings/helper' : fault === 'unsupported tool result media' ? 'tool result media' : kind === 'source' ? 'unknown original field' : 'unknown field "preparedBody"')
      expect(host.wireRequests).toEqual([])
      expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      await writeFile(join(artifacts, `guard-${kind}-${fault.replace(/\W+/g, '-')}.json`), JSON.stringify({ input, code, stdout, stderr, wireRequests: host.wireRequests, sentinelUnchanged: true }, null, 2))
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
})
