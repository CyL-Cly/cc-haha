import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const fixturePath = join(rootDir, 'scripts/quality-gate/go-parity/ui-preferences.fixture.ts')
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-ui-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'ui-fixture.exe' : 'ui-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/uipreferences/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
type Step = { operation: 'read' | 'sidebar' | 'profile' | 'project-name' | 'pet' | 'voice' | 'upload-avatar' | 'clear-avatar' | 'read-avatar'; patch?: unknown; bytes?: number[]; contentType?: string }
type Fault = { operation: string; path: string; message: string; once?: boolean }
type Scenario = { files?: Record<string, string | Uint8Array>; steps: Step[]; faults?: Fault[] }
type Output = { results: Array<{ value?: unknown; error?: { status: number; code: string; message: string } }>; trace: Array<{ operation: string; paths: string[] }>; warningCount: number }
const normalizeNonce = (path: string) => path.replace(/\.(bak-before-migration|invalid)-\d+-[a-f0-9]+$/, '.$1-<nonce>').replace(/\.tmp\.[^/]+$/, '.tmp.<nonce>')
async function withHome<T>(task: (home: string) => Promise<T>) {
  const home = await mkdtemp(join(tmpdir(), 'go-ui-parity-'))
  try { return await task(home) } finally { await rm(home, { recursive: true, force: true }) }
}
async function initialize(home: string, scenario: Scenario) {
  for (const [path, bytes] of Object.entries(scenario.files ?? {})) {
    const absolute = join(home, path)
    await mkdir(dirname(absolute), { recursive: true })
    await writeFile(absolute, bytes)
  }
}
async function run(kind: 'typescript' | 'go', home: string, scenario: Scenario): Promise<Output> {
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', fixturePath], {
    cwd: home, env: createSandboxedTestEnvironment(home),
    stdin: new Blob([JSON.stringify({ home, steps: scenario.steps, faults: scenario.faults ?? [] })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} UI fixture: ${stderr}`).toBe(0)
    const output = JSON.parse(stdout) as Output
    output.trace = output.trace.map(event => ({ ...event, paths: event.paths.map(normalizeNonce) }))
    return output
  } finally { clearTimeout(timer); child.kill() }
}
async function persisted(home: string) {
  const files: Array<{ path: string; bytes: string }> = []
  async function visit(path: string, prefix: string) {
    const entries = await readdir(path, { withFileTypes: true })
    for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
      const relative = `${prefix}/${entry.name}`
      if (entry.isDirectory()) await visit(join(path, entry.name), relative)
      else if (entry.isFile()) files.push({ path: normalizeNonce(relative), bytes: (await readFile(join(path, entry.name))).toString('hex') })
      else throw new Error('UI fixture produced non-regular config state')
    }
  }
  try { await visit(join(home, '.claude'), '.claude') } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
  }
  return files.sort((a, b) => a.path.localeCompare(b.path) || a.bytes.localeCompare(b.bytes))
}
async function snapshot(kind: 'typescript' | 'go', scenario: Scenario) {
  return withHome(async home => {
    await initialize(home, scenario)
    return { ...await run(kind, home, scenario), files: await persisted(home) }
  })
}
const path = '.claude/cc-haha/desktop-ui.json'
const old = '{"schemaVersion":3,"future":{"keep":true},"sidebar":{"projectOrder":["a",4,"a","b"],"pinnedProjects":["a"],"futureSidebar":true},"profile":{"displayName":"  Alice   Example  ","futureProfile":true},"pet":{"enabled":true,"selectedPetId":"custom:my-pet","futurePet":true},"voiceInput":{"providerId":" local ","language":"zh","futureVoice":true},"projectDisplayNames":{"__proto__":"  Prototype   name  ","  exact key  ":"Project","wrong":null}}'
const scenarios: Array<[string, Scenario]> = [
  ['missing defaults read and avatar read leave config absent', { steps: [{ operation: 'read' }, { operation: 'read-avatar' }] }],
  ['old schema, all sibling unknowns, project names survive all field patches', { files: { [path]: old }, steps: [{ operation: 'read' }, { operation: 'sidebar', patch: { hiddenProjects: ['b'], projectOrganization: 'project', futureNewSidebar: true } }, { operation: 'profile', patch: { subtitle: ' New   subtitle ', avatarFile: 'profile/avatar.jpg', newUnknown: 'ignored' } }, { operation: 'pet', patch: { collapsed: true } }, { operation: 'voice', patch: { enabled: true, downloadSource: 'mirror' } }, { operation: 'read' }] }],
  ['future safe schema and request compatibility unknowns retain same persisted bytes', { files: { [path]: old.replace('"schemaVersion":3', '"schemaVersion":999') }, steps: [{ operation: 'pet', patch: { enabled: false } }, { operation: 'read' }] }],
  ['overflow, large numbers, negative zero, duplicate keys and lone UTF16 serialize as JS', { files: { [path]: '{"schemaVersion":1e400,"future":9007199254740993,"overflow":1e400,"negative":-0,"duplicate":1,"duplicate":2,"\\ud800":"first","\\ud801":"second","pet":{"size":1e400},"sidebar":{"future":9007199254740993}}' }, steps: [{ operation: 'read' }, { operation: 'voice', patch: { enabled: true } }, { operation: 'read' }] }],
  ['profile and session UTF16 slicing may preserve a split surrogate', { files: { [path]: JSON.stringify({ profile: { displayName: 'x'.repeat(79) + '😀', subtitle: 'x'.repeat(159) + '😀' }, pet: { lastSessionId: 'x'.repeat(199) + '😀' }, voiceInput: { providerId: 'x'.repeat(79) + '😀' } }) }, steps: [{ operation: 'read' }, { operation: 'pet', patch: { showTaskPanel: true } }] }],
  ['JavaScript whitespace includes BOM and excludes NEL', { steps: [{ operation: 'profile', patch: { displayName: '\ufeffA\n\tB\ufeff', subtitle: '\u0085' } }, { operation: 'project-name', patch: { projectKey: ' \u0085 ', displayName: ' \u0085 ' } }, { operation: 'read' }] }],
  ['corrupt owned preference file quarantines original bytes then saves defaults', { files: { [path]: '{bad' }, steps: [{ operation: 'read' }, { operation: 'read' }, { operation: 'voice', patch: { enabled: true } }, { operation: 'read' }] }],
  ['wrong root shape quarantines instead of normalizing in place', { files: { [path]: '[]' }, steps: [{ operation: 'read' }, { operation: 'read' }] }],
  ['blank existing preferences report exists true without rewriting', { files: { [path]: ' \n\t\ufeff' }, steps: [{ operation: 'read' }] }],
  ['invalid UTF8 replacement preserves readable source until save', { files: { [path]: Uint8Array.from([...Buffer.from('{"future":"'), 0xff, 0xfe, 0xe2, 0x82, ...Buffer.from('"}')]) }, steps: [{ operation: 'read' }, { operation: 'sidebar', patch: {} }] }],
  ['exact project keys, prototype keys, reset and malformed payloads', { steps: [{ operation: 'project-name', patch: { projectKey: '  exact key  ', displayName: '  My   project  ' } }, { operation: 'project-name', patch: { projectKey: '__proto__', displayName: 'Prototype' } }, { operation: 'project-name', patch: { projectKey: 'constructor', displayName: 'Constructor' } }, { operation: 'project-name', patch: { projectKey: '\ud800', displayName: '\ud801' } }, { operation: 'project-name', patch: { projectKey: '  exact key  ', displayName: null } }, ...[null, [], {}, { projectKey: ' ' }, { projectKey: 'key' }, { projectKey: 'x'.repeat(4097), displayName: 'Name' }, { projectKey: 'key', displayName: 'x'.repeat(81) }].map(patch => ({ operation: 'project-name' as const, patch })), { operation: 'read' }] }],
  ['profile validation occurs after recoverable read and retains managed avatar fields', { files: { [path]: '{broken' }, steps: [{ operation: 'profile', patch: null }, ...[{ displayName: null }, { subtitle: 0 }, { displayName: ' ' }, { displayName: 'x'.repeat(79) + '😀' }, { subtitle: 'x'.repeat(161) }].map(patch => ({ operation: 'profile' as const, patch })), { operation: 'profile', patch: { displayName: 'Name', avatarFile: '../outside', avatarUpdatedAt: 'forged' } }, { operation: 'read' }] }],
  ['pet and voice malformed values use defaults, partial updates retain prior fields', { steps: [{ operation: 'pet', patch: { enabled: true, size: 250, selectedPetId: ' custom:my-pet ', lastSessionId: ' session ', futurePet: true } }, { operation: 'pet', patch: { collapsed: true } }, { operation: 'pet', patch: { size: 2.5, motionEnabled: 'wrong', selectedPetId: 'BAD', lastSessionId: '' } }, { operation: 'voice', patch: { enabled: true, providerId: ' local ', language: 'yue', downloadSource: 'official', futureVoice: true } }, { operation: 'voice', patch: { language: 'invalid', providerId: 'x'.repeat(81), downloadSource: null } }, { operation: 'sidebar', patch: [] }, { operation: 'pet', patch: null }, { operation: 'voice', patch: 'wrong' }] }],
  ['avatar bytes, MIME params, managed-field isolation, other-format removal and clear', { files: { '.claude/cc-haha/profile/keep.txt': 'protected' }, steps: [{ operation: 'upload-avatar', bytes: [0, 1, 255], contentType: ' IMAGE/PNG ; charset=utf-8 ' }, { operation: 'read-avatar' }, { operation: 'profile', patch: { avatarFile: 'profile/avatar.jpg', avatarUpdatedAt: 'forged', displayName: 'Name' } }, { operation: 'upload-avatar', bytes: [2, 3], contentType: 'image/jpeg' }, { operation: 'read-avatar' }, { operation: 'upload-avatar', bytes: [], contentType: 'image/webp' }, { operation: 'clear-avatar' }, { operation: 'read-avatar' }, { operation: 'read' }] }],
  ['unsupported and oversized avatar types reject before accessing preferences', { steps: [...['image/svg+xml', '', '\u0085image/png\u0085', 'İMAGE/PNG'].map(contentType => ({ operation: 'upload-avatar' as const, bytes: [], contentType })), { operation: 'upload-avatar', bytes: new Array(2_000_001).fill(0), contentType: 'image/png' }] }],
  ['inherited MIME lookups retain legacy validation and recovery order without accepting an image', { files: { [path]: '{bad' }, steps: [{ operation: 'upload-avatar', bytes: [], contentType: 'constructor' }, { operation: 'upload-avatar', bytes: [], contentType: '__proto__' }, { operation: 'upload-avatar', bytes: new Array(2_000_001).fill(0), contentType: 'constructor' }, { operation: 'read' }] }],
  ['missing or unsafe avatar paths return null and never access outside managed profile', { files: { [path]: '{"profile":{"avatarFile":"../outside","avatarUpdatedAt":0}}' }, steps: [{ operation: 'read-avatar' }, { operation: 'profile', patch: { displayName: 'Name' } }, { operation: 'read' }] }],
  ['configured avatar file that is missing returns null', { files: { [path]: '{"profile":{"avatarFile":"profile/avatar.png"}}' }, steps: [{ operation: 'read-avatar' }] }],
  ['actual upgrade runs before UI read and protected shared user settings remain untouched', { files: { [path]: old, '.claude/cc-haha/settings.json': '{"env":null,"futureManaged":true}', '.claude/settings.json': '{"futureUser":true}' }, steps: [{ operation: 'read' }, { operation: 'pet', patch: { enabled: false } }] }],
]
for (const operation of ['access', 'read', 'mkdir', 'write', 'rename']) scenarios.push([`exact ${operation} failure preserves previous preferences and source cleanup`, { files: { [path]: old }, steps: [{ operation: 'sidebar', patch: {} }, { operation: 'read' }], faults: [{ operation, path: operation === 'mkdir' ? '.claude/cc-haha' : path, message: `fixture ${operation} denied`, once: true }] }])
scenarios.push(['best-effort quarantine failure leaves exists true and original bytes', { files: { [path]: '{bad' }, steps: [{ operation: 'read' }], faults: [{ operation: 'rename', path, message: 'fixture quarantine denied' }] }])
scenarios.push(['failed avatar replacement leaves old image and preferences intact', { files: { [path]: '{"profile":{"avatarFile":"profile/avatar.png"}}', '.claude/cc-haha/profile/avatar.png': 'old' }, steps: [{ operation: 'upload-avatar', bytes: [1, 2], contentType: 'image/jpeg' }, { operation: 'read-avatar' }], faults: [{ operation: 'rename', path: '.claude/cc-haha/profile/avatar.jpg.tmp.', message: 'fixture avatar denied' }] }])
scenarios.push(['preferences failure after image replacement preserves source non-transactional behavior', { files: { [path]: '{"profile":{"avatarFile":"profile/avatar.png"}}', '.claude/cc-haha/profile/avatar.png': 'old' }, steps: [{ operation: 'upload-avatar', bytes: [1, 2], contentType: 'image/jpeg' }, { operation: 'read-avatar' }], faults: [{ operation: 'write', path: path + '.tmp.', message: 'fixture preferences denied' }] }])
scenarios.push(['failed clear deletion is best effort and read error preserves profile metadata', { files: { [path]: '{"profile":{"avatarFile":"profile/avatar.png"}}', '.claude/cc-haha/profile/avatar.png': 'old' }, steps: [{ operation: 'read-avatar' }, { operation: 'clear-avatar' }, { operation: 'read-avatar' }], faults: [{ operation: 'read', path: '.claude/cc-haha/profile/avatar.png', message: 'fixture read denied' }, { operation: 'unlink', path: '.claude/cc-haha/profile/avatar.', message: 'fixture remove denied' }] }])
const names = Object.fromEntries(Array.from({ length: 2001 }, (_, index) => [String(2000 - index), 'name']))
scenarios.push(['2,000 entry cap follows integer enumeration and permits existing-key replacements', { files: { [path]: JSON.stringify({ projectDisplayNames: { other: 'Other', ...names }, sidebar: { projectOrder: Array.from({ length: 2005 }, (_, index) => `key-${index}`) } }) }, steps: [{ operation: 'read' }, { operation: 'project-name', patch: { projectKey: 'new', displayName: 'New' } }, { operation: 'project-name', patch: { projectKey: '0', displayName: 'Updated' } }, { operation: 'project-name', patch: { projectKey: '1', displayName: null } }, { operation: 'project-name', patch: { projectKey: 'new', displayName: 'New' } }, { operation: 'read' }] }])

for (const [name, scenario] of scenarios) test(`Desktop UI TS self replay and Go business parity: ${name}`, async () => {
  const expected = await snapshot('typescript', scenario)
  expect(await snapshot('typescript', scenario)).toEqual(expected)
  expect(await snapshot('go', scenario)).toEqual(expected)
}, 40_000)
test('actual TS -> Go -> TS UI and avatar writes preserve future siblings and exact project keys', async () => {
  await withHome(async home => {
    await initialize(home, { files: { [path]: old }, steps: [] })
    await run('typescript', home, { steps: [{ operation: 'project-name', patch: { projectKey: ' exact ', displayName: 'Project' } }, { operation: 'upload-avatar', bytes: [0, 1, 255], contentType: 'image/png' }] })
    await run('go', home, { steps: [{ operation: 'voice', patch: { downloadSource: 'mirror' } }, { operation: 'profile', patch: { displayName: 'Go name' } }] })
    const observed = (await run('typescript', home, { steps: [{ operation: 'read' }, { operation: 'read-avatar' }, { operation: 'pet', patch: { collapsed: true } }] })).results
    expect(observed[0]!.value).toMatchObject({ exists: true, preferences: { future: { keep: true }, profile: { displayName: 'Go name', avatarFile: 'profile/avatar.png', futureProfile: true }, voiceInput: { downloadSource: 'mirror', futureVoice: true }, projectDisplayNames: { ' exact ': 'Project' } } })
    expect(observed[1]!.value).toEqual({ bytes: '0001ff', contentType: 'image/png' })
    expect((await run('go', home, { steps: [{ operation: 'read' }] })).results[0]!.value).toMatchObject({ preferences: { pet: { collapsed: true, futurePet: true } } })
  })
}, 40_000)
