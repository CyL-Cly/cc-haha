import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { createRequestRetryHost } from './request-retry-host'
import { requestRetryCases, requestRetryProfile } from './request-retry-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifacts = join(root, 'artifacts/go-parity/request-retry')
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'request-retry-build-'))
  executable = join(buildHome, 'request-retry.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/requestretry'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(item: typeof requestRetryCases[number], kind: 'source' | 'go' = 'source') {
  const home = await mkdtemp(join(tmpdir(), 'request-retry-source-'))
  const host = createRequestRetryHost(item.mode)
  try {
    await mkdir(join(home, '.claude'))
    const { extraEnv, ...original } = item
    const input = { ...requestRetryProfile, ...original, home, env: {
      ANTHROPIC_BASE_URL: host.origin, ANTHROPIC_API_KEY: 'offline-original-env-key', API_TIMEOUT_MS: '3000',
      CLAUDE_CODE_MAX_RETRIES: '2', CLAUDE_CODE_DISABLE_FAST_MODE: '1', ...extraEnv,
    }, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/request-retry.fixture.ts')] : [executable, '-test.run=^TestRequestRetryFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, {
        CC_HAHA_REQUEST_RETRY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production',
        CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0',
      }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('REQUEST_RETRY_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(item.mode === 'abort-before' ? [] : ['read'])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed },
          { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn },
        ])
        expect(result.sourceOnly.pairingEvidence).toHaveLength(1)
        expect(result.sourceOnly.sdk).toBe('0.128.0')
      }
      const server = await host.settled()
      const observation = { ...result.observation, server }
      expect(server.closedModelConnections).toBe(server.requests)
      expect(observation.preparationCalls).toBe(1)
      expect(observation.requests.length).toBe(host.wireRequests.length)
      for (const [index, request] of observation.requests.entries()) {
        const wire = host.wireRequests[index]
        expect({ path: wire.path, body: wire.body }).toEqual({ path: request.path, body: request.body })
        expect(request.headers.find(([name]: string[]) => name === 'x-stainless-package-version')?.[1]).toBe('0.128.0')
        const received = new Headers(wire.headers)
        for (const [name, value] of request.headers) expect(received.get(name), `${kind} socket header ${name}`).toBe(value)
      }
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, item.name.replace(/\W+/g, '-') + '-' + kind + '.json'), JSON.stringify({ input: { ...input, home: '<private-home>', env: { ...input.env, ANTHROPIC_BASE_URL: '<per-child-loopback>' } }, observation, sourceOnly: { ...result.sourceOnly, wireRequests: host.wireRequests } }, null, 2))
      return observation
    } finally { clearTimeout(timer) }
  } finally { host.close(); await rm(home, { recursive: true, force: true }) }
}

for (const item of requestRetryCases) test(`actual Source self + independent Go physical creation retry: ${item.name}`, async () => {
  const first = await observe(item)
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item) }])
  assertTraceEqual([{ kind: 'query', value: first }], [{ kind: 'query', value: await observe(item, 'go') }])
}, 65000)


test('strict physical trace rejects behavioral counterexamples', async () => {
  const original = await observe(requestRetryCases[0])
  const mutants = [
    (value: any) => value.requests[1].body += ' ',
    (value: any) => value.requests[2].headers.find(([name]: string[]) => name === 'x-api-key')[1] = 'Source-prepared-key',
    (value: any) => value.clients++,
    (value: any) => value.sleeps[1] = 1000,
    (value: any) => value.notices.reverse(),
    (value: any) => value.uuidCallsOnRequest[1]++,
    (value: any) => value.preparationCalls++,
    (value: any) => value.normalizedHistory[0].messageSource += ' ',
    (value: any) => value.server.closedModelConnections--,
    (value: any) => value.outcome = 'consumer_stopped',
  ]
  for (const mutate of mutants) {
    const changed = structuredClone(original)
    mutate(changed)
    expect(() => assertTraceEqual([{ kind: 'query', value: original }], [{ kind: 'query', value: changed }])).toThrow()
  }
}, 30000)

for (const kind of ['source', 'go'] as const) for (const fault of ['HOME mismatch', 'config mismatch', 'cwd mismatch', 'missing profile', 'public origin', 'unknown credential source', 'missing original key', ...kind === 'source' ? ['native', 'network', 'outside-read'] : ['prepared shortcut']]) test(`${kind} fails closed before escaped request/state: ${fault}`, async () => {
  const home = await mkdtemp(join(tmpdir(), 'request-retry-guard-'))
  const outside = await mkdtemp(join(tmpdir(), 'request-retry-sentinel-'))
  const sentinel = join(outside, 'sentinel.json')
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(sentinel, '{"protected":"unchanged"}')
    const input = { ...requestRetryProfile, name: 'guard', mode: 'sequence', home: fault === 'HOME mismatch' ? outside : home,
      env: { ANTHROPIC_BASE_URL: fault === 'public origin' ? 'https://fixture.invalid' : 'http://127.0.0.1:1', ANTHROPIC_API_KEY: fault === 'missing original key' ? '' : 'offline-original-env-key', CLAUDE_CODE_DISABLE_FAST_MODE: '1', ...fault === 'unknown credential source' ? { CLAUDE_CODE_API_KEY_HELPER_TTL_MS: '1000' } : {} },
      runtime: { platform: process.platform, arch: process.arch, version: process.version },
      ...['native', 'network', 'outside-read'].includes(fault) ? { probe: fault, probePath: sentinel } : {},
      ...fault === 'prepared shortcut' ? { preparedBody: '{}' } : {},
    }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/request-retry.fixture.ts')] : [executable, '-test.run=^TestRequestRetryFixture$'], {
      cwd: fault === 'cwd mismatch' ? outside : home,
      env: createSandboxedTestEnvironment(home, { CC_HAHA_REQUEST_RETRY_FIXTURE: fault === 'missing profile' ? 'denied' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ...fault === 'config mismatch' ? { CLAUDE_CONFIG_DIR: outside } : {} }),
      stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 10000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).not.toContain('REQUEST_RETRY_RESULT:')
      expect(stdout + stderr).toContain(fault === 'prepared shortcut' ? 'unknown field "preparedBody"' : fault === 'unknown credential source' ? 'environment' : fault === 'public origin' ? 'loopback' : fault === 'missing original key' ? kind === 'source' ? 'original-env-key' : 'missing original environment API key' : ['native', 'network', 'outside-read'].includes(fault) ? 'blocked' : fault === 'missing profile' && kind === 'source' ? 'explicit private' : 'disposable HOME/config')
      expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
}, 15000)
