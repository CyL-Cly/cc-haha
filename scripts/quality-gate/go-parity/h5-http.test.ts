import { afterAll, beforeAll, expect, test } from 'bun:test'
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { h5HttpBase, h5HttpManagedFile, h5HttpScenarios, type H5HttpOutput, type H5HttpScenario } from './h5-http-fixtures'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string
let nativeExecutable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'h5-http-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  nativeExecutable = join(buildHome, process.platform === 'win32' ? 'native.exe' : 'native')
  const env = await createGoBuildEnvironment(rootDir, buildHome)
  for (const [output, source] of [[executable, './internal/httpapi/testdata/h5fixture'], [nativeExecutable, './internal/httpapi/testdata/h5nativeerrors']]) {
    const child = Bun.spawn(['go', 'build', '-o', output, source], { cwd: join(rootDir, 'backend'), env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 180_000)
    try {
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
      expect(code, stderr).toBe(0)
    } finally { clearTimeout(timer) }
  }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Snapshot = H5HttpOutput & { files: Array<{ path: string; bytes: string }>; stderr: string }
const normalizeNonce = (path: string) => path.replace(/\.(bak-before-migration|invalid)-\d+-[a-f0-9]+$/, '.$1-<nonce>').replace(/\.tmp\.[^/]+$/, '.tmp.<nonce>')
const json = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('hex')
async function withHome<T>(task: (home: string) => Promise<T>) {
  const home = await mkdtemp(join(tmpdir(), 'h5-http-fixture-'))
  try { return await task(home) } finally { await rm(home, { recursive: true, force: true }) }
}
async function initialize(home: string, scenario: H5HttpScenario) {
  for (const path of scenario.directories ?? []) await mkdir(join(home, path), { recursive: true })
  for (const [path, bytes] of Object.entries(scenario.files ?? {})) {
    const target = join(home, path)
    await mkdir(dirname(target), { recursive: true })
    await writeFile(target, bytes)
  }
  for (const [path, mode] of Object.entries(scenario.modes ?? {})) await chmod(join(home, path), mode)
}
async function run(kind: 'typescript' | 'go', home: string, scenario: H5HttpScenario) {
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/h5-http.fixture.ts')], {
    cwd: home, env: createSandboxedTestEnvironment(home),
    stdin: new Blob([JSON.stringify({ ...scenario, home })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} H5 HTTP fixture: ${stderr}`).toBe(0)
    const output = JSON.parse(stdout) as H5HttpOutput
    output.trace = output.trace.map(event => ({ ...event, paths: event.paths.map(normalizeNonce) }))
    return { output, stderr }
  } finally { clearTimeout(timer); child.kill() }
}
async function persisted(home: string) {
  const files: Array<{ path: string; bytes: string }> = []
  async function visit(path: string, prefix: string) {
    for (const entry of (await readdir(path, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
      const relative = `${prefix}/${entry.name}`
      if (entry.isDirectory()) await visit(join(path, entry.name), relative)
      else if (entry.isFile()) files.push({ path: normalizeNonce(relative), bytes: (await readFile(join(path, entry.name))).toString('hex') })
      else throw new Error('H5 HTTP fixture produced non-regular config state')
    }
  }
  try { await visit(join(home, '.claude'), '.claude') } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
  return files.sort((a, b) => a.path.localeCompare(b.path) || a.bytes.localeCompare(b.bytes))
}
async function snapshot(kind: 'typescript' | 'go', scenario: H5HttpScenario, sharedHome: string): Promise<Snapshot> {
  // One disposable absolute HOME makes known error-body path bytes comparable.
  // Only nondeterministic temporary filenames are normalized, never messages.
  await rm(join(sharedHome, '.claude'), { recursive: true, force: true })
  await initialize(sharedHome, scenario)
  const result = await run(kind, sharedHome, scenario)
  // Restore readability only after the real handler has observed the OS error,
  // so the evidence snapshot/cleanup does not bypass this native fixture.
  for (const path of Object.keys(scenario.modes ?? {})) await chmod(join(sharedHome, path), 0o600)
  return { ...result.output, stderr: result.stderr, files: await persisted(sharedHome) }
}

for (const scenario of h5HttpScenarios) test(`H5 HTTP actual TS self replay and Go exact contract: ${scenario.name}`, async () => {
  await withHome(async home => {
    const source = await snapshot('typescript', scenario, home)
    expect(source.diagnostics).toEqual([])
    expect(source.stderr).toBe('')
    expect(await snapshot('typescript', scenario, home)).toEqual(source)
    expect(await snapshot('go', scenario, home)).toEqual(source)
  })
}, 45_000)

test('H5 raw authorization predicates retain source slash behavior independently of the handler', async () => {
  await withHome(async home => {
    const scenario = h5HttpScenarios.find(value => value.name.startsWith('raw control'))!
    const output = await snapshot('typescript', scenario, home)
    expect(output.policy.map(value => [value.pathname, value.resource, value.control, value.requiresLocalCredential])).toEqual([
      ['/api//h5-access', 'h5-access', false, false],
      [h5HttpBase + '/', 'h5-access', true, true],
      [h5HttpBase + '/verify', 'h5-access', false, false],
      [h5HttpBase + '/verify/', 'h5-access', true, true],
      [h5HttpBase + '/verify/extra', 'h5-access', true, true],
      [h5HttpBase + '/%76erify', 'h5-access', true, true],
    ])
    expect(output.results.map(result => result.status)).toEqual([200, 200, 401, 401, 401, 404])
    // These predicates are source evidence, not a test of a composed Go server
    // auth middleware. The repeated-slash policy weakness is tracked separately.
  })
}, 45_000)

test('actual TS -> Go -> TS H5 token writes, managed unknowns and grace cache are interoperable', async () => {
  await withHome(async home => {
    await initialize(home, { name: 'interop', files: { [h5HttpManagedFile]: '{"env":{},"future":{"value":9007199254740993,"unit":"\\ud800"},"h5Access":{"disconnectGraceSeconds":42}}', '.claude/settings.json': '{"protected":"user"}' }, steps: [] })
    const source = await run('typescript', home, { name: 'source enable', steps: [{ method: 'POST', path: h5HttpBase + '/enable' }] })
    const token = JSON.parse(Buffer.from(source.output.results[0]!.body, 'hex').toString()).token as string
    const update = await run('go', home, { name: 'go writes', initialRefresh: true, steps: [
      { method: 'POST', path: h5HttpBase + '/verify', headers: [['Authorization', `bearer\t${token}`]] },
      { method: 'PUT', path: h5HttpBase, body: json({ disconnectGraceSeconds: 8, allowedOrigins: ['https://example.com/path'], fixedPort: 28670 }) },
    ] })
    expect(update.output.results[0]!.body).toBe(json({ ok: true }))
    expect(update.output.results.map(value => value.graceMs)).toEqual([42_000, 8_000])
    const readScenario: H5HttpScenario = { name: 'both read real state', initialRefresh: true, steps: [{ method: 'GET', path: h5HttpBase }, { method: 'POST', path: h5HttpBase + '/verify', headers: [['Authorization', `Bearer ${token}`]] }] }
    expect((await run('go', home, readScenario)).output).toEqual((await run('typescript', home, readScenario)).output)
    const actual = JSON.parse(await readFile(join(home, h5HttpManagedFile), 'utf8'))
    expect(actual.future).toEqual({ value: 9007199254740992, unit: '\ud800' })
    expect(actual.h5Access.disconnectGraceSeconds).toBe(8)
    expect(await readFile(join(home, '.claude/settings.json'), 'utf8')).toBe('{"protected":"user"}')
  })
}, 45_000)

for (const fault of ['mkdir', 'random6', 'random32'] as const) test(`managed ${fault} outside catch keeps unknown diagnostics, clock/RNG consumption and raw pending artifacts explicit`, async () => {
  const cause = fault === 'mkdir' ? 'fixture mkdir denied' : 'fixture random denied'
  const scenario: H5HttpScenario = { name: `unknown managed ${fault}`, initialRefresh: true, trackWritePreparation: true, files: { [h5HttpManagedFile]: '{"env":{},"committed":true}' }, steps: [{ method: 'POST', path: h5HttpBase + '/enable' }], ...(fault === 'mkdir' ? { faults: [{ operation: 'mkdir', path: '.claude/cc-haha', message: cause, once: true }] } : { randomErrorSize: fault === 'random6' ? 6 : 32 }) }
  await withHome(async home => {
    const source = await snapshot('typescript', scenario, home)
    const go = await snapshot('go', scenario, home)
    expect(source.results).toEqual([{ status: 500, headers: { 'content-type': 'application/json;charset=utf-8' }, body: json({ error: 'INTERNAL_ERROR', message: 'An unexpected error occurred' }), graceMs: 30_000 }])
    expect(go.results).toEqual(source.results)
    expect(go.diagnostics).toEqual(source.diagnostics)
    expect(source.diagnostics).toEqual([{ type: 'api_unhandled_error', severity: 'error', summary: cause }])
    expect(go.randomCalls).toEqual(source.randomCalls)
    expect(source.randomCalls).toEqual(fault === 'random32' ? [32] : [32, 6])
    const preparation = fault === 'random32' ? ['random:32'] : ['random:32', 'clock', 'random:6', ...(fault === 'mkdir' ? ['mkdir:.claude/cc-haha'] : [])]
    for (const result of [source, go]) {
      expect(result.writePreparation?.slice(0, preparation.length)).toEqual(preparation)
      expect(result.trace.some(event => event.operation === 'unlink' && event.paths[0]!.startsWith(h5HttpManagedFile))).toBe(false)
      expect(result.files.find(file => file.path === h5HttpManagedFile)?.bytes).toBe(Buffer.from('{"env":{},"committed":true}').toString('hex'))
      expect(result.stderr).toContain('[Server] Unexpected error:')
    }
    expect(source.files.some(file => file.path.endsWith('/diagnostics.jsonl'))).toBe(true)
    expect(go.files.some(file => file.path.endsWith('/diagnostics.jsonl'))).toBe(false)
    expect(go.stderr).not.toBe(source.stderr)
    const artifactDir = join(rootDir, 'artifacts/go-parity/h5-http', fault, new Date().toISOString().replace(/[:.]/g, '-'))
    await mkdir(artifactDir, { recursive: true })
    await writeFile(join(artifactDir, 'source.raw.stderr.txt'), source.stderr)
    await writeFile(join(artifactDir, 'go.raw.stderr.txt'), go.stderr)
    await writeFile(join(artifactDir, 'source.diagnostic-storage.json'), JSON.stringify(source.files.filter(file => file.path.includes('/diagnostics/')), null, 2))
    await writeFile(join(artifactDir, 'go.diagnostic-callback.json'), JSON.stringify(go.diagnostics, null, 2))
    await writeFile(join(artifactDir, 'pending.json'), JSON.stringify({ diagnosticsStorage: 'not integrated', errorStack: 'not equivalent', serverOuterAuthorization: 'not composed', startupAndWebSocketGraceCache: 'not wired', benchmarkPerformance: 'not measured', sourceRawSlashAuthorization: 'separate policy follow-up required' }, null, 2))
  })
}, 45_000)

test('native OS errors compare actual Bun syscall/path rendering without replacing native Go causes', async () => {
  await withHome(async home => {
    await mkdir(join(home, 'directory'))
    await writeFile(join(home, 'file'), 'fixture')
    await writeFile(join(home, 'denied'), 'fixture')
    type Step = { name: string; operation: string; path: string; target?: string; sourceIsFile?: boolean }
    const steps: Step[] = [
      { name: 'read directory', operation: 'read', path: 'directory' },
      { name: 'write directory', operation: 'write', path: 'directory' },
      { name: 'mkdir file', operation: 'mkdir', path: 'file' },
      { name: 'mkdir under file', operation: 'mkdir', path: 'file/child' },
      { name: 'mkdir deeply under file', operation: 'mkdir', path: 'file/child/grandchild' },
      { name: 'read not directory', operation: 'read', path: 'file/child' },
      { name: 'read missing', operation: 'read', path: 'missing' },
      { name: 'rename missing', operation: 'rename', path: 'missing', target: 'renamed' },
      { name: 'rename file onto directory', operation: 'rename', path: 'file', target: 'directory', sourceIsFile: true },
    ]
    // chmod cannot produce Unix read/write-denial fixtures on Windows. That
    // platform needs native ACL fixtures; this check never claims that evidence.
    if (process.platform !== 'win32') {
      await chmod(join(home, 'denied'), 0)
      steps.push({ name: 'read denied', operation: 'read', path: 'denied' }, { name: 'write denied', operation: 'write', path: 'denied' })
    }
    type Result = { name: string; rendered: string; native: string }
    async function capture(kind: 'typescript' | 'go') {
      const child = Bun.spawn(kind === 'go' ? [nativeExecutable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/h5-http-native-errors.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home), stdin: new Blob([JSON.stringify({ home, steps })]), stdout: 'pipe', stderr: 'pipe' })
      const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code, stderr).toBe(0)
        expect(stderr).toBe('')
        return JSON.parse(stdout) as Result[]
      } finally { clearTimeout(timer); child.kill() }
    }
    try {
      const source = await capture('typescript')
      expect(await capture('typescript')).toEqual(source)
      const go = await capture('go')
      expect(go.map(({ name, rendered }) => ({ name, rendered }))).toEqual(source.map(({ name, rendered }) => ({ name, rendered })))
      expect(go.some(value => value.native !== value.rendered)).toBe(true)
      const artifacts = join(rootDir, 'artifacts/go-parity/h5-http/native-errors', new Date().toISOString().replace(/[:.]/g, '-'))
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, 'source.json'), JSON.stringify({ platform: process.platform, samples: source }, null, 2))
      await writeFile(join(artifacts, 'go.json'), JSON.stringify({ platform: process.platform, samples: go }, null, 2))
      await writeFile(join(artifacts, 'scope.json'), JSON.stringify({ nativeHost: process.platform, unobservedErrnos: 'original Go text retained', otherNativeHosts: 'not run', windowsACL: 'not run', errorStack: 'not equivalent', diagnosticsStorage: 'not integrated' }, null, 2))
    } finally { await chmod(join(home, 'denied'), 0o600) }
  })
}, 45_000)
