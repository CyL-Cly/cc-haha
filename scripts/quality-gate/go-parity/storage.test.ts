import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const fixturePath = join(rootDir, 'scripts/quality-gate/go-parity/storage.fixture.ts')
let buildHome: string
let executable: string

beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-storage-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'storage-fixture.exe' : 'storage-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/storage/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'),
    env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally {
    clearTimeout(timer)
  }
}, 190_000)

afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Kind = 'typescript' | 'go'
type Snapshot = { value: Record<string, unknown>; warnings: string[] }

async function run(kind: Kind, home: string, operation: 'read' | 'update', patch?: Record<string, unknown>): Promise<Snapshot> {
  const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir() })
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', fixturePath], {
    cwd: home, env,
    stdin: new Blob([JSON.stringify({ operation, path: join(env.CLAUDE_CONFIG_DIR!, 'settings.json'), patch })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 10_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} storage fixture: ${stderr}`).toBe(0)
    return JSON.parse(stdout) as Snapshot
  } finally {
    clearTimeout(timer)
    child.kill()
  }
}

async function withHome<T>(task: (home: string, path: string) => Promise<T>): Promise<T> {
  const home = await mkdtemp(join(tmpdir(), 'go-storage-parity-'))
  const path = join(home, '.claude', 'settings.json')
  try {
    await mkdir(join(home, '.claude'), { recursive: true })
    return await task(home, path)
  } finally {
    await rm(home, { recursive: true, force: true })
  }
}

async function snapshot(kind: Kind, raw: Uint8Array | string | undefined) {
  return withHome(async (home, path) => {
    if (raw !== undefined) await writeFile(path, raw)
    const result = await run(kind, home, 'read')
    const files = await readdir(join(home, '.claude'))
    const quarantines = files.filter(name => name.startsWith('settings.json.invalid-'))
    const original = files.includes('settings.json') ? Array.from(await readFile(path)) : null
    const preserved = await Promise.all(quarantines.map(async name => Array.from(await readFile(join(home, '.claude', name)))))
    return { value: result.value, warningCount: result.warnings.length, original, preserved, otherFiles: files.filter(name => name !== 'settings.json' && !quarantines.includes(name)) }
  })
}

const legacy = await readFile(join(rootDir, 'backend/internal/storage/testdata/legacy-settings.json'))
const corrupt = await readFile(join(rootDir, 'backend/internal/storage/testdata/corrupt-settings.json'))
const invalidString = Buffer.concat([Buffer.from('{"future":"'), Buffer.from([0xff, 0xfe, 0xe2, 0x82]), Buffer.from('"}')])
const fixtures: Array<[string, Uint8Array | string | undefined]> = [
  ['missing', undefined],
  ['empty', ''],
  ['ASCII whitespace', ' \t\r\n'],
  ['JS whitespace including BOM', '\u00a0\u1680\u2000\u2028\u2029\u3000\ufeff'],
  ['old settings with unknown values and large numeric lexemes', legacy],
  ['corrupt old settings', corrupt],
  ['null shape', 'null'],
  ['array shape', '[]'],
  ['primitive shape', '42'],
  ['multiple JSON documents', '{} {}'],
  ['invalid UTF-8 inside a string', invalidString],
  ['invalid UTF-8 outside JSON', Buffer.from([0xff, 0xfe])],
  ['JSON whitespace excludes U+0085', '\u0085'],
  ['duplicate fields retain final value', '{"field":{"old":true},"field":{"new":true}}'],
  ['BOM preceding nonempty JSON is rejected', '\ufeff{}'],
]

for (const [name, raw] of fixtures) {
  test(`recoverable storage TS self replay and Go parity: ${name}`, async () => {
    const expected = await snapshot('typescript', raw)
    expect(await snapshot('typescript', raw)).toEqual(expected)
    expect(await snapshot('go', raw)).toEqual(expected)
  }, 35_000)
}

test('actual TS SettingsService -> Go atomic write -> TS read and save preserves old and future fields', async () => {
  await withHome(async (home, path) => {
    const initial = {
      env: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '1', LEGACY_OTHER_ENV: 'keep' },
      desktopTerminal: { shell: 'system', unknownFutureSetting: { keep: true } },
      unknownFuturePreference: { array: [null, false, { nested: '中文 😀 <&>' }], loneSurrogate: '\ud800' },
    }
    const fromTS = await run('typescript', home, 'update', initial)
    expect(fromTS.value).toEqual(initial)
    const tsBytes = await readFile(path)
    expect((await run('go', home, 'read')).value).toEqual(fromTS.value)
    expect(await readFile(path)).toEqual(tsBytes)

    const goValue = (await run('go', home, 'update', { language: '中文 😀', futureGoPreference: { keep: true } })).value
    expect(goValue).toEqual({ ...initial, language: '中文 😀', futureGoPreference: { keep: true } })
    const goBytes = await readFile(path, 'utf8')
    expect(goBytes.endsWith('\n')).toBe(true)
    expect(goBytes).toContain('<&>')
    expect((await run('typescript', home, 'read')).value).toEqual(goValue)
    expect(await readFile(path, 'utf8')).toBe(goBytes)

    const fromTSAgain = (await run('typescript', home, 'update', { desktopTerminal: { shell: 'future-shell' } })).value
    expect(fromTSAgain).toEqual({ ...goValue, desktopTerminal: { shell: 'future-shell', unknownFutureSetting: { keep: true } } })
    expect((await run('go', home, 'read')).value).toEqual(fromTSAgain)
    expect(fromTSAgain.schemaVersion).toBeUndefined()
  })
}, 35_000)

test('Go atomic rewrite preserves unknown large numeric lexemes and remains readable by TS', async () => {
  await withHome(async (home, path) => {
    await writeFile(path, legacy)
    const expected = (await run('typescript', home, 'read')).value
    await run('go', home, 'update', { language: '中文 😀' })
    const raw = await readFile(path, 'utf8')
    expect(raw).toContain('9007199254740993123456789')
    expect(raw).toContain('1.234567890123456789e+100')
    expect(raw).toContain('\\ud800')
    expect((await run('typescript', home, 'read')).value).toEqual({ ...expected, language: '中文 😀' })
  })
}, 35_000)
