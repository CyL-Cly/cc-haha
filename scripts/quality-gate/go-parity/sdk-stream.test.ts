import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { sdkStreamPendingScenarios, sdkStreamScenarios, type SDKStreamObservation, type SDKStreamScenario } from './sdk-stream-fixtures'
import { assertTraceEqual, firstTraceDifference, type TraceEvent } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let buildHome: string, executable: string
async function record(scenario: SDKStreamScenario, kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(allocationRoot, 'sdk-stream-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'typescript' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/sdk-stream.fixture.ts')] : [executable, '-test.run=^TestSDKStreamFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SDK_STREAM_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
      stdin: new Blob([JSON.stringify({ ...scenario, home })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const raw = kind === 'typescript' ? stdout : stdout.split('\n').find(line => line.startsWith('SDK_STREAM_RESULT:'))?.slice('SDK_STREAM_RESULT:'.length)
      expect(raw).toBeDefined()
      const parsed = JSON.parse(raw!)
      if (kind === 'typescript') {
        expect(parsed.sdkVersion).toBe('0.80.0')
        expect(parsed.engineVersion).toBe('1.3.14')
        expect(parsed.capabilities).toEqual({ blocked: [], secureReads: [], absentReads: [] })
      }
      const observation = parsed.observation as SDKStreamObservation
      if (kind === 'go') {
        const artifacts = join(root, 'artifacts/go-parity/sdk-stream-go')
        await mkdir(artifacts, { recursive: true })
        await writeFile(join(artifacts, encodeURIComponent(scenario.name) + '.json'), JSON.stringify({ scenario, observation }, null, 2))
      }
      const trace: TraceEvent[] = [{ kind: 'transport', value: observation }]
      return { observation, trace, sdkVersion: kind === 'typescript' ? parsed.sdkVersion as string : undefined, engineVersion: kind === 'typescript' ? parsed.engineVersion as string : undefined }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

describe('SDK 0.80.0 actual raw dispatch Source self replay', () => {
  for (const scenario of sdkStreamScenarios) test(scenario.name, async () => {
    const first = await record(scenario)
    assertTraceEqual(first.trace, (await record(scenario)).trace)
    const artifacts = join(root, 'artifacts/go-parity/sdk-stream-source')
    await mkdir(artifacts, { recursive: true })
    await writeFile(join(artifacts, encodeURIComponent(scenario.name) + '.json'), JSON.stringify({ sdkVersion: first.sdkVersion, engineVersion: first.engineVersion, scenario, observation: first.observation }, null, 2))
  }, 45_000)
})

describe('SDK raw stream deliberate comparator mutations', () => {
  for (const field of ['yield', 'error', 'headers', 'diagnostics'] as const) test(field, async () => {
    const scenario = sdkStreamScenarios.find(value => value.name === (field === 'error' ? 'sse-error-full-body' : field === 'diagnostics' ? 'syntax-objectkey' : 'six-message-events-and-legacy'))!
    const source = await record(scenario)
    const mutation = structuredClone(source.observation)
    if (field === 'yield') mutation.yields[0]!.source = '{}'
    else if (field === 'error') mutation.error!.message += '!'
    else if (field === 'headers') mutation.headers.pop()
    else mutation.diagnostics[0]!.valueSource += '!'
    expect(() => assertTraceEqual(source.trace, [{ kind: 'transport', value: mutation }])).toThrow('First semantic difference')
  })
})

describe('SDK Source pre-import isolation guards', () => {
  for (const change of ['flag', 'config', 'bun-options', 'wire', 'header-injection', 'config-symlink'] as const) test(change, async () => {
    const home = await mkdtemp(join(allocationRoot, 'sdk-stream-negative-'))
    const outside = await mkdtemp(join(allocationRoot, 'sdk-stream-sentinel-'))
    try {
      const env = createSandboxedTestEnvironment(home, { CC_HAHA_SDK_STREAM_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot })
      const input = { ...sdkStreamScenarios[0]!, home }
      if (change === 'flag') delete env.CC_HAHA_SDK_STREAM_FIXTURE
      else if (change === 'config') env.CLAUDE_CONFIG_DIR = join(home, 'wrong-config')
      else if (change === 'bun-options') env.BUN_OPTIONS = ''
      else if (change === 'wire') input.wireBytes = [256]
      else if (change === 'header-injection') input.headers = [['x-future', 'value\r\nlocation: https://example.invalid']]
      else { await rm(join(home, '.claude'), { recursive: true, force: true }); await symlink(outside, join(home, '.claude'), process.platform === 'win32' ? 'junction' : 'dir') }
      const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/sdk-stream.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).toBe('')
      expect(stderr).toContain(['config', 'bun-options', 'config-symlink'].includes(change) ? 'requires disposable HOME/config before production imports' : 'requires the explicit valid offline wire profile')
    } finally { await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
  })
})

describe('SDK 0.80.0 actual raw dispatch Go parity', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'sdk-stream-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'provider.test.exe' : 'provider.test')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/provider'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of sdkStreamScenarios) test(scenario.name, async () => assertTraceEqual((await record(scenario)).trace, (await record(scenario, 'go')).trace), 45_000)
  for (const scenario of sdkStreamPendingScenarios) test(`KNOWN PENDING response metadata: ${scenario.name}`, async () => {
    const source = await record(scenario)
    assertTraceEqual(source.trace, (await record(scenario)).trace)
    // This is a recorded incompatibility, not a Go-equivalent profile. Go
    // net/http discards the entire close-containing Connection header.
    const candidate = await record(scenario, 'go')
    expect(firstTraceDifference(source.trace, candidate.trace)?.path).toContain('.headers')
    expect(candidate.observation.yields).toEqual(source.observation.yields)
    expect(() => assertTraceEqual(source.trace, candidate.trace)).toThrow('First semantic difference')
    const artifacts = join(root, 'artifacts/go-parity/sdk-stream-pending')
    await mkdir(artifacts, { recursive: true })
    await writeFile(join(artifacts, scenario.name + '.json'), JSON.stringify({ status: 'pending', scenario, source: source.observation, go: candidate.observation }, null, 2))
  }, 45_000)
})
