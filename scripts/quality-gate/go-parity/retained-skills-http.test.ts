import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { retainedSkillsCases } from './retained-skills-fixtures'
import { createRequestRetryHost } from './request-retry-host'
import { requestRetryProfile } from './request-retry-fixtures'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { assertTraceEqual } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url)), artifacts = join(root, 'artifacts/go-parity/retained-skills/http')
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'retained-skills-http-build-')); executable = join(buildHome, 'request-retry.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/requestretry'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
const original = (name: string) => JSON.parse(retainedSkillsCases.find(row => row.name === name)!.source) as unknown[]
const orphan = { type: 'assistant', message: { id: 'historical-id', type: 'message', role: 'assistant', model: 'deepseek-flash', content: [{ type: 'tool_use', id: 'orphan', name: 'HistoricalTool', input: { original: true } }], stop_reason: 'tool_use', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } }, uuid: 'old-assistant', timestamp: '2023-11-14T22:13:20.000Z' }
const profiles = [
  { name: 'duplicate empty Skills and listing before first request', history: original('retained-no-tools') },
  { name: 'attachments after users bubble before physical retry', history: original('attachments-after-users') },
  { name: 'empty attachments allocate no IDs', history: original('empty-skill-no-allocation') },
  { name: 'typed malformed attachment caught before real requests', history: original('shadowed-value-drop') },
  { name: 'retained lone UTF16 and orphan pairing prepared once', history: [orphan, { type: 'attachment', uuid: 'retained', timestamp: '2000-01-01Z', attachment: JSON.parse(retainedSkillsCases.find(row => row.name === 'invoked-lone-utf16')!.source) }, JSON.parse(requestRetryProfile.historySources[0]!)] },
]
async function observe(row: typeof profiles[number], kind: 'source' | 'go' = 'source') {
  const home = await mkdtemp(join(tmpdir(), 'retained-skills-http-')), host = createRequestRetryHost('sequence')
  try {
    await mkdir(join(home, '.claude'))
    const input = { ...requestRetryProfile, name: row.name, mode: 'sequence', historySources: row.history.map(value => JSON.stringify(value)), home,
      env: { ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', API_TIMEOUT_MS: '3000', CLAUDE_CODE_MAX_RETRIES: '2', CLAUDE_CODE_DISABLE_FAST_MODE: '1' }, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/request-retry.fixture.ts')] : [executable, '-test.run=^TestRequestRetryFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_REQUEST_RETRY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, row.name.replace(/\W+/g, '-') + '-' + kind + '.json'), JSON.stringify({ original: { ...input, home: '<private>', env: { ...input.env, ANTHROPIC_BASE_URL: '<exact-private-loopback>' } }, code, stdout, stderr }, null, 2))
      expect(code, stdout + stderr).toBe(0); expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('REQUEST_RETRY_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([{ operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed }, { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn }])
        expect(result.sourceOnly.pairingEvidence).toHaveLength(1)
      }
      const observation = { ...result.observation, server: await host.settled() }
      expect(observation.server.closedModelConnections).toBe(3)
      expect(observation.preparationCalls).toBe(1)
      expect(observation.requests).toHaveLength(3)
      expect(observation.sleeps).toEqual([500, 2000])
      // Independently captured physical body/path and every resolved header;
      // Bun automatic fetch headers are retained as Source-only host evidence.
      for (const [i, request] of observation.requests.entries()) {
        const wire = host.wireRequests[i]
        expect({ body: wire.body, path: wire.path }).toEqual({ body: request.body, path: request.path })
        const headers = new Headers(wire.headers)
        for (const [name, value] of request.headers) expect(headers.get(name), name).toBe(value)
      }
      expect(new Set(observation.requests.map((r: any) => r.body)).size).toBe(1)
      expect(new Set(observation.uuidCallsOnRequest.map((n: number, i: number) => n - i)).size).toBe(1)
      return observation
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }) }
}
for (const row of profiles) test('actual original retained history → Prepare once → real HTTP creation retry: ' + row.name, async () => {
  const source = await observe(row)
  assertTraceEqual([{ kind: 'model-request', value: source }], [{ kind: 'model-request', value: await observe(row) }])
  assertTraceEqual([{ kind: 'model-request', value: source }], [{ kind: 'model-request', value: await observe(row, 'go') }])
}, 65000)
test('physical request comparator detects rendered-body and retry lifecycle mutations', async () => {
  const source = await observe(profiles[0])
  for (const mutate of [(v: any) => { v.requests[0].body += ' ' }, (v: any) => { v.preparationCalls++ }, (v: any) => { v.uuidCallsOnRequest[1]++ }, (v: any) => { v.normalizedHistory[0].messageSource += ' ' }, (v: any) => { v.requests[1].headers.reverse() }]) {
    const changed = structuredClone(source); mutate(changed)
    expect(() => assertTraceEqual([{ kind: 'model-request', value: source }], [{ kind: 'model-request', value: changed }])).toThrow()
  }
}, 30000)
