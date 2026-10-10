import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { probeCli, cliBuildFeatures } from './cliInventory'
import { authFixtures, type AuthStep } from './cliauth-fixtures'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string, executable: string
const parserCache = new Map<string, Awaited<ReturnType<typeof probeCli>>>()
const pending: unknown[] = []
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-cliauth-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'cliauth-fixture.exe' : 'cliauth-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/cliauth/testdata/fixture'], { cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill(), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => {
  if (pending.length) { const path = join(rootDir, 'artifacts/go-parity/cliauth'); await mkdir(path, { recursive: true }); await writeFile(join(path, 'source-only-pending-diagnostics.json'), JSON.stringify({ scope: 'Malformed raw secure-storage/date/log diagnostics are Source observations, not Go compatibility passes', pending }, null, 2) + '\n') }
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
})
async function actualParsed(steps: AuthStep[]) {
  const output = []
  for (const step of steps) {
    const args = step.args ?? ['auth', 'status', '--openai'], key = JSON.stringify(args)
    let result = parserCache.get(key)
    if (!result) { result = await probeCli(args); parserCache.set(key, result) }
    if (result.exitCode === 86) output.push({ ...step, parsed: result.parsed })
    else output.push({ ...step, parser: { stdout: result.stdout, stderr: result.stderr, exitCode: result.exitCode } })
  }
  return output
}
async function run(source: boolean, steps: AuthStep[], nativeStdio = false) {
  const home = await mkdtemp(join(tmpdir(), 'cliauth-parity-'))
  try {
    const child = Bun.spawn(source ? [process.execPath, '--no-env-file', ...cliBuildFeatures.map(feature => `--feature=${feature}`), join(rootDir, 'scripts/quality-gate/go-parity/cliauth.fixture.ts')] : [executable], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_LOCAL_BUILD_TIME: '2000-01-01T00:00:00.000Z', CLAUDE_CODE_LOCAL_VERSION: '999.0.0-local', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', NO_COLOR: '1', TERM: 'dumb' }),
      stdin: new Blob([JSON.stringify({ homeDir: home, nativeStdio, steps: source ? await actualParsed(steps) : steps })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 25_000)
    try {
      const [code, stdoutBuffer, stderrBuffer] = await Promise.all([child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).arrayBuffer()])
      const stdout = Buffer.from(stdoutBuffer).toString('utf8'), stderr = Buffer.from(stderrBuffer).toString('utf8')
      if (nativeStdio) return { exitCode: code, stdout, stderr, stdoutBytes: [...new Uint8Array(stdoutBuffer)], stderrBytes: [...new Uint8Array(stderrBuffer)], evidence: JSON.parse(await readFile(join(home, 'native-evidence.json'), 'utf8')) }
      expect(code, stderr).toBe(0); expect(stderr).toBe(''); return JSON.parse(stdout)
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}

for (const name of ['missing-credentials', 'secure-store-json-and-text', 'unicode-identity-json-and-text', 'numeric-expiry-1e400']) {
  const fixture = authFixtures.find(value => value.name === name)!
  test('unintercepted native process stdout/stderr/exit matches actual TS handler: ' + name, async () => {
    const step = fixture.steps[0]!
    for (const args of [['auth', 'status', '--openai'], ['auth', 'status', '--openai', '--text']]) {
      // Infinity has a pending RangeError in text mode, already recorded above.
      if (name === 'numeric-expiry-1e400' && args.includes('--text')) continue
      const reference = await run(true, [{ ...step, args }], true)
      expect(reference.evidence.blocked).toEqual([])
      expect(await run(false, [{ ...step, args }], true)).toEqual(reference)
    }
  }, 30_000)
}
for (const fixture of authFixtures) {
  if (fixture.sourceOnly) {
    test('Source-only pending raw diagnostics (no Go compatibility claim): ' + fixture.name, async () => {
      const reference = await run(true, fixture.steps)
      expect(reference.blocked).toEqual([])
      expect(reference.results.every((result: any) => result.pendingDiagnostic && result.exitCode === null)).toBe(true)
      expect(reference.results.map((result: any) => result.pendingDiagnostic.name)).toEqual(['TypeError', 'TypeError', 'RangeError', 'RangeError'])
      expect(reference.results.every((result: any) => typeof result.pendingDiagnostic.rawStack === 'string')).toBe(true)
      const candidate = await run(false, fixture.steps)
      expect(candidate.results.every((result: any) => result.exitCode === null && result.pendingDiagnostic?.stdioComplete === false)).toBe(true)
      pending.push({ name: fixture.name, reference, candidate, compatible: false })
    }, 40_000)
  } else {
    test('actual production OpenAI auth status/storage + Cobra business parity: ' + fixture.name, async () => {
      const reference = await run(true, fixture.steps)
      const repeated = await run(true, fixture.steps)
      expect(reference.blocked).toEqual([])
      expect(reference.results).toEqual(repeated.results)
      const candidate = await run(false, fixture.steps)
      expect(candidate.results).toEqual(reference.results)
      if (reference.loggedErrors.length) pending.push({ name: fixture.name, loggedErrors: reference.loggedErrors })
    }, 40_000)
  }
}

test.each(['home', 'config', 'cwd'])('auth status fixture rejects invalid %s before importing any production handler', async boundary => {
  const home = await mkdtemp(join(tmpdir(), 'cliauth-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'auth-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(join(rootDir, 'scripts/quality-gate/go-parity/cliauth.fixture.ts'))})`)
    const sentinel = join(home, 'sentinel.json')
    await writeFile(sentinel, '{"protected":"unchanged"}')
    const child = Bun.spawn([process.execPath, '--no-env-file', loader], {
      cwd: boundary === 'cwd' ? rootDir : home,
      env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), ...(boundary === 'home' ? { HOME: join(home, 'wrong') } : {}), ...(boundary === 'config' ? { CLAUDE_CONFIG_DIR: join(home, 'wrong-config') } : {}) }),
      stdin: new Blob([JSON.stringify({ homeDir: home, steps: [] })]), stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stderr).toContain('fixture requires disposable HOME/config before production imports')
    expect(stdout).not.toContain('PRODUCTION_IMPORT_REACHED')
    expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
  } finally { await rm(home, { recursive: true, force: true }) }
})

test('the Go fixture rejects an escaped token path before modifying a protected file', async () => {
  const home = await mkdtemp(join(tmpdir(), 'cliauth-go-guard-')), protectedHome = await mkdtemp(join(tmpdir(), 'cliauth-protected-'))
  try {
    const sentinel = join(protectedHome, 'sentinel.json')
    await writeFile(sentinel, '{"protected":"unchanged"}')
    const child = Bun.spawn([executable], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir() }), stdin: new Blob([JSON.stringify({ homeDir: home, steps: [{ fileEnv: sentinel, file: 'must never write' }] })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).toBe('')
    expect(stderr).toContain('auth fixture path must remain inside disposable HOME')
    expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
  } finally { await rm(home, { recursive: true, force: true }); await rm(protectedHome, { recursive: true, force: true }) }
})
