import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'desktop-ui-http-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/httpapi/testdata/desktopuifixture'], {
    cwd: join(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { method: string; path: string; segments?: string[]; headers?: Array<[string, string]>; body?: string; readError?: { name: string; message: string } }
type Fault = { operation: string; path: string; message: string; once?: boolean }
type Scenario = { files?: Record<string, string | Uint8Array>; steps: Step[]; faults?: Fault[]; petToken?: string }
type Output = { results: Array<{ status: number; headers: Record<string, string>; body: string }>; trace: Array<{ operation: string; paths: string[] }>; warningCount: number; diagnostics: Array<{ type: string; severity: string; summary: string }> }
type Snapshot = Output & { files: Array<{ path: string; bytes: string }>; stderr: string }
const normalizeNonce = (path: string) => path.replace(/\.(bak-before-migration|invalid)-\d+-[a-f0-9]+$/, '.$1-<nonce>').replace(/\.tmp\.[^/]+$/, '.tmp.<nonce>')
const json = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('hex')
const raw = (value: string) => Buffer.from(value).toString('hex')
const request = (method: string, path: string, body?: unknown, headers?: Array<[string, string]>): Step => ({ method, path, ...(body === undefined ? {} : { body: json(body) }), headers })
const base = '/api/desktop-ui/preferences'
const preferenceFile = '.claude/cc-haha/desktop-ui.json'
const old = '{"schemaVersion":3,"future":{"<>&":9007199254740993},"profile":{"displayName":" Old ","futureProfile":"\\ud800"},"pet":{"futurePet":1e400},"sidebar":{"futureSidebar":true},"voiceInput":{"futureVoice":true},"projectDisplayNames":{"__proto__":"Prototype"}}'

async function withHome<T>(task: (home: string) => Promise<T>) {
  const home = await mkdtemp(join(tmpdir(), 'desktop-ui-http-fixture-'))
  try { return await task(home) } finally { await rm(home, { recursive: true, force: true }) }
}
async function initialize(home: string, scenario: Scenario) {
  for (const [path, bytes] of Object.entries(scenario.files ?? {})) {
    const target = join(home, path)
    await mkdir(dirname(target), { recursive: true })
    await writeFile(target, bytes)
  }
}
async function run(kind: 'typescript' | 'go', home: string, scenario: Scenario) {
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/desktop-ui-http.fixture.ts')], {
    cwd: home, env: createSandboxedTestEnvironment(home),
    stdin: new Blob([JSON.stringify({ home, steps: scenario.steps, faults: scenario.faults ?? [], petToken: scenario.petToken ?? '' })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} HTTP fixture: ${stderr}`).toBe(0)
    const output = JSON.parse(stdout) as Output
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
      else throw new Error('HTTP fixture produced non-regular config state')
    }
  }
  try { await visit(join(home, '.claude'), '.claude') } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
  return files.sort((a, b) => a.path.localeCompare(b.path) || a.bytes.localeCompare(b.bytes))
}
async function snapshot(kind: 'typescript' | 'go', scenario: Scenario, sharedHome?: string): Promise<Snapshot> {
  const capture = async (home: string) => {
    // Reuse exactly one disposable absolute path for strict error bytes; do
    // not hide source messages containing that path with a text replacement.
    await rm(join(home, '.claude'), { recursive: true, force: true })
    await initialize(home, scenario)
    const result = await run(kind, home, scenario)
    return { ...result.output, stderr: result.stderr, files: await persisted(home) }
  }
  return sharedHome ? capture(sharedHome) : withHome(capture)
}

const scenarios: Array<[string, Scenario]> = [
  ['missing defaults, pet projection and missing avatar bytes', { steps: [request('GET', base), request('GET', base + '/pet'), request('GET', base + '/profile/avatar')] }],
  ['endpoint errors, method precedence and explicit segment behavior', { steps: [
    ...['', '/unknown', '/profile/unknown'].map(path => request('GET', '/api/desktop-ui' + (path || ''))),
    ...['POST', 'HEAD', 'OPTIONS', 'TRACE'].map(method => request(method, base)),
    ...['sidebar', 'profile', 'project-display-name', 'pet', 'profile/avatar'].map(path => request('PATCH', base + '/' + path)),
    { ...request('GET', '/ignored'), segments: ['other', 'resource', 'preferences'] },
    { ...request('GET', base), segments: [] },
    { ...request('PUT', base), segments: ['api', 'desktop-ui', 'preferences', ''] },
  ] }],
  ['WHATWG percent escapes, dot segments, repeated slashes and ignored query', { steps: ['/api//desktop-ui/preferences/', '/api/desktop-ui/../desktop-ui/preferences?unused=1', '/api/desktop-ui/%2e%2e/desktop-ui/preferences', '/api/desktop-ui/%70references', base + '/%70et', base + '/profile/avatar/extra', base + '/pet/extra', base + '/中文'].map(path => request('GET', path)) }],
  ['full preferences patches retain ordered unknowns and JS serialization', { files: { [preferenceFile]: old }, steps: [request('GET', base), request('PUT', base + '/sidebar', { projectOrder: ['b', 'a', 'b'], future: '<>&' }), request('PUT', base + '/profile', { displayName: ' New   name ', avatarFile: '../ignored' }), request('PUT', base + '/pet', { enabled: true }), request('PUT', base + '/project-display-name', { projectKey: '\ud800', displayName: '<>&\ud801' }), request('GET', base)] }],
  ['null array number boolean and string bodies retain each service validation', { steps: ['sidebar', 'profile', 'pet', 'project-display-name'].flatMap(path => [null, [], 42, true, 'text'].map(body => request('PUT', base + '/' + path, body))) }],
  ['invalid JSON and UTF8/BOM decoding preserve response bytes and read ordering', { files: { [preferenceFile]: old }, steps: ['', '{bad', '{} {}', ' \ufeff{}'].map(body => ({ ...request('PUT', base + '/sidebar'), body: raw(body) })).concat([
    { ...request('PUT', base + '/sidebar'), body: raw('\ufeff{"unknown":true}') },
    { ...request('PUT', base + '/sidebar'), body: Buffer.from([...Buffer.from('{"unknown":"'), 255, 254, ...Buffer.from('"}')]).toString('hex') },
    { ...request('PUT', base + '/pet'), body: raw('{"size":1e400,"future":9007199254740993,"overflow":1e400,"duplicate":1,"duplicate":2}') },
  ]) }],
  ['corrupt recovery occurs only after valid request parsing and business read', { files: { [preferenceFile]: '{bad' }, steps: [{ ...request('PUT', base + '/sidebar'), body: raw('{bad') }, request('PUT', base + '/project-display-name', null), request('PUT', base + '/profile', null), request('GET', base)] }],
  ['actual upgrades preserve protected shared settings', { files: { [preferenceFile]: old, '.claude/settings.json': '{"protected":"user"}', '.claude/cc-haha/settings.json': '{"env":null,"future":true}' }, steps: [request('GET', base), request('PUT', base + '/pet', {})] }],
  ['pet credential controls only PUT response projection with exact bearer semantics', { petToken: ' fake-pet-token ', files: { [preferenceFile]: old }, steps: [request('GET', base + '/pet'), ...[[], [['Authorization', 'Bearer fake-pet-token']], [['Authorization', 'Bearer fake-pet-token ignored']], [['Authorization', 'Bearer  fake-pet-token']], [['Authorization', 'bearer fake-pet-token']], [['Authorization', 'Bearer wrong']], [['Authorization', 'Bearer fake-pet-token'], ['Authorization', 'Bearer wrong']]].map(headers => request('PUT', base + '/pet', { collapsed: true }, headers as Array<[string, string]>))] }],
  ['missing pet credential and forged query token retain full response', { steps: [request('PUT', base + '/pet?token=fake-pet-token', {}, [['Authorization', 'Bearer fake-pet-token']])] }],
  ['avatar raw bytes explicit headers zero byte upload and deletion', { files: { '.claude/cc-haha/profile/keep.txt': 'protected' }, steps: [
    { ...request('PUT', base + '/profile/avatar'), body: '00ff0180', headers: [['Content-Type', ' IMAGE/PNG ;charset=binary ']] }, request('GET', base + '/profile/avatar?v=old'),
    { ...request('PUT', base + '/profile/avatar/extra'), body: '', headers: [['Content-Type', 'image/webp']] }, request('GET', base + '/profile/avatar'), request('DELETE', base + '/profile/avatar'), request('GET', base + '/profile/avatar'),
  ] }],
  ['duplicate unsupported inherited and oversized MIME retains exact source errors', { files: { [preferenceFile]: '{bad' }, steps: [
    { ...request('PUT', base + '/profile/avatar'), body: '', headers: [['Content-Type', 'image/png'], ['content-type', 'image/jpeg']] },
    ...['image/svg+xml', 'image/png,image/jpeg', 'constructor', '__proto__'].map(type => ({ ...request('PUT', base + '/profile/avatar'), body: '', headers: [['Content-Type', type] as [string, string]] })),
    { ...request('PUT', base + '/profile/avatar'), body: Buffer.alloc(2_000_001).toString('hex'), headers: [['Content-Type', 'image/png']] },
  ] }],
  ['request stream abort maps JSON to 400 and avatar to empty 499', { steps: [{ name: 'AbortError', message: 'fixture cancelled' }, { name: 'Error', message: 'The operation was aborted' }, { name: 'Error', message: 'The connection was closed.' }].flatMap(readError => [{ ...request('PUT', base + '/sidebar'), readError }, { ...request('PUT', base + '/profile/avatar'), headers: [['Content-Type', 'image/png'] as [string, string]], readError }]) }],
]
for (const operation of ['access', 'read', 'write', 'rename']) scenarios.push([`managed ${operation} failure preserves exact API bytes and persistence`, { files: { [preferenceFile]: old }, steps: [request('PUT', base + '/sidebar', {}), request('GET', base)], faults: [{ operation, path: preferenceFile, message: `fixture ${operation} denied`, once: true }] }])
scenarios.push(['avatar preferences write failure retains existing non-transactional side effects', { files: { [preferenceFile]: '{"profile":{"avatarFile":"profile/avatar.png"}}', '.claude/cc-haha/profile/avatar.png': 'old' }, steps: [{ ...request('PUT', base + '/profile/avatar'), body: '0102', headers: [['Content-Type', 'image/jpeg']] }, request('GET', base + '/profile/avatar')], faults: [{ operation: 'write', path: preferenceFile + '.tmp.', message: 'fixture preference denied' }] }])

for (const [name, scenario] of scenarios) test(`Desktop UI HTTP TS self replay and Go exact bytes: ${name}`, async () => {
  await withHome(async home => {
    const expected = await snapshot('typescript', scenario, home)
    expect(expected.diagnostics).toEqual([])
    expect(expected.stderr).toBe('')
    expect(await snapshot('typescript', scenario, home)).toEqual(expected)
    expect(await snapshot('go', scenario, home)).toEqual(expected)
  })
}, 45_000)

test('actual TS -> Go -> TS HTTP writes retain React response bytes and shared avatar state', async () => {
  await withHome(async home => {
    await initialize(home, { files: { [preferenceFile]: old }, steps: [] })
    const sourceWrite = await run('typescript', home, { steps: [{ ...request('PUT', base + '/profile/avatar'), body: '00ff0180', headers: [['Content-Type', 'image/png']] }] })
    expect(sourceWrite.output.results[0]!.status).toBe(200)
    const goWrite = await run('go', home, { petToken: 'fake-pet-token', steps: [request('PUT', base + '/pet', { enabled: true }, [['Authorization', 'Bearer fake-pet-token']]), request('PUT', base + '/project-display-name', { projectKey: ' exact ', displayName: 'Go name' })] })
    expect(Buffer.from(goWrite.output.results[0]!.body, 'hex').toString()).toStartWith('{"ok":true,"pet":')
    expect(goWrite.output.results[1]!.body).toBe(json({ ok: true, projectKey: ' exact ', displayName: 'Go name' }))
    const sourceRead = await run('typescript', home, { steps: [request('GET', base), request('GET', base + '/profile/avatar')] })
    const goRead = await run('go', home, { steps: [request('GET', base), request('GET', base + '/profile/avatar')] })
    expect(goRead.output.results).toEqual(sourceRead.output.results)
    expect(sourceRead.output.results[1]).toEqual({ status: 200, headers: { 'content-type': 'image/png', 'cache-control': 'no-store' }, body: '00ff0180' })
    expect(JSON.parse(Buffer.from(sourceRead.output.results[0]!.body, 'hex').toString())).toMatchObject({ exists: true, preferences: { future: { '<>&': 9007199254740992 }, profile: { futureProfile: '\ud800', avatarFile: 'profile/avatar.png' }, pet: { enabled: true, futurePet: null }, projectDisplayNames: { ' exact ': 'Go name' } } })
  })
}, 45_000)

test('unknown errors keep response and diagnostic event metadata; raw stacks and diagnostic storage remain explicit integration evidence', async () => {
  const scenario: Scenario = { steps: [request('PUT', base + '/sidebar', {}), { ...request('PUT', base + '/profile/avatar'), headers: [['Content-Type', 'image/png']], readError: { name: 'Error', message: 'fixture stream failed' } }], faults: [{ operation: 'mkdir', path: '.claude/cc-haha', message: 'fixture mkdir denied', once: true }] }
  const source = await snapshot('typescript', scenario)
  const go = await snapshot('go', scenario)
  expect(go.results).toEqual(source.results)
  expect(go.diagnostics).toEqual(source.diagnostics)
  expect(source.diagnostics).toEqual(['fixture mkdir denied', 'fixture stream failed'].map(summary => ({ type: 'api_unhandled_error', severity: 'error', summary })))
  expect(source.files.some(file => file.path.endsWith('/diagnostics.jsonl'))).toBe(true)
  expect(go.files.some(file => file.path.endsWith('/diagnostics.jsonl'))).toBe(false)
  expect(source.stderr).toContain('[Server] Unexpected error:')
  expect(go.stderr).toContain('[Server] Unexpected error:')
  expect(source.stderr).not.toBe(go.stderr)
  const artifactDir = join(rootDir, 'artifacts/go-parity/desktop-ui-http', new Date().toISOString().replace(/[:.]/g, '-'))
  await mkdir(artifactDir, { recursive: true })
  await writeFile(join(artifactDir, 'source.raw.stderr.txt'), source.stderr)
  await writeFile(join(artifactDir, 'go.raw.stderr.txt'), go.stderr)
  await writeFile(join(artifactDir, 'source.diagnostic-storage.json'), JSON.stringify(source.files.filter(file => file.path.includes('/diagnostics/')), null, 2))
  await writeFile(join(artifactDir, 'go.diagnostic-callback.json'), JSON.stringify(go.diagnostics, null, 2))
  await writeFile(join(artifactDir, 'pending.json'), JSON.stringify({ diagnosticsStorage: 'not integrated', errorStack: 'not equivalent', serverTransport: 'not integrated', benchmarkPerformance: 'not measured' }, null, 2))
}, 45_000)
