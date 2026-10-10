import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { protocolFixture, protocolFixtureNames, type ProtocolFixture } from './protocol-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let buildHome: string
let executable: string
let transportExecutable: string

beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-protocol-parity-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'protocol.test.exe' : 'protocol.test')
  transportExecutable = join(buildHome, process.platform === 'win32' ? 'provider.test.exe' : 'provider.test')
  const env = await createGoBuildEnvironment(rootDir, buildHome)
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/protocol'], {
    cwd: resolve(rootDir, 'backend'), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    env,
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
  const transport = Bun.spawn(['go', 'test', '-c', '-o', transportExecutable, './internal/provider'], {
    cwd: resolve(rootDir, 'backend'), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe', env,
  })
  const transportTimer = setTimeout(() => transport.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([transport.exited, new Response(transport.stderr).text(), new Response(transport.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(transportTimer) }
}, 370_000)

afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function record(kind: 'typescript' | 'go' | 'go-http', fixture: ProtocolFixture, observeHTTP = false): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), `protocol-parity-${kind}-`))
  const path = join(home, 'protocol-fixture.json')
  await writeFile(path, JSON.stringify(fixture))
  const env = createSandboxedTestEnvironment(home, {
    CC_HAHA_PROTOCOL_FIXTURE: path, CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production',
    ANTHROPIC_API_KEY: 'fixture-key', ANTHROPIC_MODEL: 'fixture-model',
    CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: '0',
    CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0',
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    ...(observeHTTP ? { CC_HAHA_PROTOCOL_HTTP_OBSERVATION: '1' } : {}),
  })
  const command = kind === 'go' ? [executable, '-test.run=^TestProtocolParityFixture$'] : kind === 'go-http' ? [transportExecutable, '-test.run=^TestProtocolTransportParityFixture$'] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/protocol-legacy.ts')]
  const child = Bun.spawn(command, {
    cwd: home, env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0)
    if (kind === 'typescript') {
      const evidence = stdout.split('\n').find(line => line.startsWith('TS_PROTOCOL_CAPABILITIES:'))
      expect(evidence, stdout + stderr).toBeDefined()
      const capabilities = JSON.parse(evidence!.slice('TS_PROTOCOL_CAPABILITIES:'.length)) as { blocked: string[]; secureReads: string[]; absentReads: Array<{ operation: string; path: string }> }
      expect(capabilities.blocked).toEqual([])
      expect(capabilities.secureReads.length).toBeGreaterThan(0)
      expect(capabilities.secureReads.every(call => call === 'read' || call === 'readAsync')).toBeTrue()
      const [settingsPath, dropInPath] = absentManagedSettingsFixturePaths()
      expect(capabilities.absentReads).toEqual([
        { operation: 'lstatSync', path: settingsPath }, { operation: 'lstatSync', path: settingsPath },
        { operation: 'openSync', path: settingsPath }, { operation: 'readdirSync', path: dropInPath },
      ])
      const artifacts = join(rootDir, 'artifacts/go-parity/protocol-isolation')
      await mkdir(artifacts, { recursive: true })
      const key = createHash('sha256').update(JSON.stringify(fixture)).digest('hex')
      await writeFile(join(artifacts, `${key}${observeHTTP ? '-http' : '-codec'}.json`), JSON.stringify({ profile: 'actual-source-empty-credentials-absent-managed-policy', capabilities }, null, 2))
    }
    const prefix = kind === 'typescript' ? 'TS_PROTOCOL_RESULT:' : 'GO_PROTOCOL_RESULT:'
    const line = stdout.split('\n').find(line => line.startsWith(prefix))
    expect(line, stdout + stderr).toBeDefined()
    const serializedResult = line!.slice(prefix.length)
    return [{ kind: 'transport', value: { decoded: JSON.parse(serializedResult), source: canonicalJsonSource(serializedResult) } }]
  } finally {
    clearTimeout(timer)
    child.kill()
    await child.exited
    await rm(home, { recursive: true, force: true })
  }
}

for (const name of protocolFixtureNames) {
  test(`Real TS API self replay and Go Anthropic commit codec: ${name}`, async () => {
    const fixture = await protocolFixture(name)
    const expected = await record('typescript', fixture)
    assertTraceEqual(expected, await record('typescript', fixture))
    assertTraceEqual(expected, await record('go', fixture))
  }, 40_000)
  test(`Real TS API self replay and Go HTTP stream transport: ${name}`, async () => {
    const fixture = await protocolFixture(name)
    const expected = await record('typescript', fixture, true)
    assertTraceEqual(expected, await record('typescript', fixture, true))
    assertTraceEqual(expected, await record('go-http', fixture))
  }, 40_000)
}

async function rejectedSource(home: string, path: string, options: { cwd?: string; overrides?: Record<string, string>; preload?: string } = {}) {
  const child = Bun.spawn([process.execPath, '--no-env-file', ...(options.preload ? ['--preload', options.preload] : []), join(rootDir, 'scripts/quality-gate/go-parity/protocol-legacy.ts')], {
    cwd: options.cwd ?? home, env: createSandboxedTestEnvironment(home, {
      CC_HAHA_PROTOCOL_FIXTURE: path, CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot,
      NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
      CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', ANTHROPIC_API_KEY: 'fixture-key', ...options.overrides,
    }), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(1)
    expect(stdout).toBe('')
    expect(stderr).not.toContain('PRODUCTION_IMPORT_TRIPWIRE')
    return stderr
  } finally { clearTimeout(timer); child.kill(); await child.exited }
}

async function importTripwire(directory: string) {
  const path = join(directory, 'import-tripwire.ts')
  await writeFile(path, `import { mock } from 'bun:test'\nmock.module(${JSON.stringify(join(rootDir, 'src/bootstrap/state.ts'))}, () => { throw new Error('PRODUCTION_IMPORT_TRIPWIRE') })\n`)
  return path
}

for (const failure of ['allocation root', 'CWD', 'user profile', 'env loading', 'symlink home', 'symlink fixture'] as const) {
  test(`protocol rejects ${failure} before production imports`, async () => {
    const parent = await mkdtemp(join(allocationRoot, 'protocol-isolation-boundary-'))
    try {
      const actualHome = join(parent, 'home')
      await mkdir(actualHome)
      const home = failure === 'symlink home' ? join(parent, 'alias') : actualHome
      if (failure === 'symlink home') await symlink(actualHome, home, 'dir')
      const path = join(home, 'protocol-fixture.json')
      const sentinel = join(parent, 'sentinel.json')
      const bytes = JSON.stringify({ wire: '' })
      await writeFile(sentinel, bytes)
      if (failure === 'symlink fixture') await symlink(sentinel, path, 'file')
      else await writeFile(path, bytes)
      const preload = await importTripwire(parent)
      const stderr = await rejectedSource(home, path, { preload, ...(failure === 'CWD' ? { cwd: parent } : {}), overrides: {
        ...(failure === 'allocation root' ? { CC_HAHA_FIXTURE_TEMP_ROOT: '' } : {}),
        ...(failure === 'user profile' ? { USERPROFILE: parent } : {}),
        ...(failure === 'env loading' ? { BUN_OPTIONS: '' } : {}),
      } })
      expect(stderr).toContain(failure === 'symlink fixture' ? 'Protocol fixture file requires a regular file inside disposable HOME' : 'Protocol fixture requires disposable HOME/config before production imports')
      expect(await readFile(sentinel, 'utf8')).toBe(bytes)
    } finally { await rm(parent, { recursive: true, force: true }) }
  })
}

for (const [probeCapability, blocked] of [
  ['subprocess', 'subprocess:execFileSync'], ['native', 'native:spawn'], ['network', 'network:fetch'],
  ['proxy-origin', 'network:fetch'], ['wrong-port', 'network:fetch'], ['credentials', 'network:fetch'],
] as const) {
  test(`protocol blocks ${probeCapability} before production imports`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'protocol-isolation-capability-'))
    try {
      const path = join(home, 'protocol-fixture.json')
      await writeFile(path, JSON.stringify({ wire: '', probeCapability }))
      expect(await rejectedSource(home, path, { preload: await importTripwire(home) })).toContain('Client options fixture blocked ' + blocked)
    } finally { await rm(home, { recursive: true, force: true }) }
  })
}

test('protocol loopback grant rejects redirects before contacting another private origin', async () => {
  const home = await mkdtemp(join(allocationRoot, 'protocol-isolation-redirect-'))
  let requests = 0
  const target = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch: () => { requests++; return new Response('outside-grant') } })
  try {
    const path = join(home, 'protocol-fixture.json')
    await writeFile(path, JSON.stringify({ wire: '', probeCapability: 'redirect', redirectTarget: target.url.origin + '/outside-grant' }))
    expect(await rejectedSource(home, path, { preload: await importTripwire(home) })).toContain('Protocol fixture refused redirect:')
    expect(requests).toBe(0)
  } finally { target.stop(true); await rm(home, { recursive: true, force: true }) }
})
