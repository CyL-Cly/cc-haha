import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, utimes, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
const fixtureEnvironment = { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'session-reader-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const env = await createGoBuildEnvironment(rootDir, buildHome)
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessionstore/testdata/fixture'], { cwd: join(rootDir, 'backend'), env, stdout: 'pipe', stderr: 'pipe' })
  const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
  expect(code, stderr).toBe(0)
}, 180_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { operation: string; cursorFrom?: number; previous?: boolean; cursor?: string; limit?: number; full?: boolean; startOffset?: number; endOffset?: number; maxRecordBytes?: number; failConsumer?: boolean; bytes?: string; raw?: string; modifyCursor?: Record<string, unknown>; legacy?: boolean }
type Mutation = { index: number; operation: 'append' | 'rewrite'; bytes: string; preserveTime?: boolean }
const hex = (value: string) => Buffer.from(value).toString('hex')
async function run(kind: 'typescript' | 'go', home: string, steps: Step[], maxRead?: number, mutateOnRead?: Mutation, hashResults?: boolean, failCloseCount?: number) {
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/session-reader.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment), stdin: new Blob([JSON.stringify({ home, steps, maxRead, mutateOnRead, hashResults, failCloseCount })]), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill(), 30_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind}: ${stderr}`).toBe(0)
    expect(stderr).toBe('')
    expect(JSON.parse(stdout)).toEqual({ result: 'reader-result.json' })
    return JSON.parse(await readFile(join(home, 'reader-result.json'), 'utf8')) as { results: Array<{ bytes?: string; error?: unknown }>; trace: unknown[] }
  } finally { clearTimeout(timer); child.kill() }
}
async function compare(initial: string | Buffer, steps: Step[], maxRead?: number, mutateOnRead?: Mutation, hashResults?: boolean, failCloseCount?: number) {
  const home = await mkdtemp(join(tmpdir(), 'session-reader-case-'))
  try {
    await mkdir(join(home, '.claude'))
    const path = join(home, 'session.jsonl')
    const initialize = async () => { await writeFile(path, initial); await utimes(path, 1_700_000_000, 1_700_000_000) }
    await initialize()
    const source = await run('typescript', home, steps, maxRead, mutateOnRead, hashResults, failCloseCount)
    const final = await readFile(path)
    await initialize()
    expect(await run('typescript', home, steps, maxRead, mutateOnRead, hashResults, failCloseCount)).toEqual(source)
    await initialize()
    expect(await run('go', home, steps, maxRead, mutateOnRead, hashResults, failCloseCount)).toEqual(source)
    expect(await readFile(path)).toEqual(final)
    return source
  } finally { await rm(home, { recursive: true, force: true }) }
}

test('actual source pages whole unknown records, numeric keys, duplicate properties and lone UTF16 units', async () => {
  const value = await compare('{"uuid":"a","2":2,"1":1,"future":9007199254740993,"x":1,"x":2,"text":"\\ud800"}\r\n\ninvalid\nfalse\n[]\n{"uuid":"b","message":{"content":[]}}\n{"uuid":"c","value":1e400}', [{ operation: 'page', limit: 1 }, { operation: 'page', cursorFrom: 0, limit: 1 }, { operation: 'page', cursorFrom: 1 }, { operation: 'page', cursorFrom: 2, previous: true }, { operation: 'page', full: true }, { operation: 'stream' }], 17)
  expect(value.results.every(result => result.bytes)).toBe(true)
}, 45_000)
test('empty transcript, whitespace, scalars and invalid-only sources preserve completeness', async () => {
  for (const bytes of ['', '\n\r\n', 'true\nnull\n[]\n1\n', 'invalid\n{"partial":']) await compare(bytes, [{ operation: 'page' }, { operation: 'stream' }])
}, 45_000)
test('append preserves frozen cursors and unknown cursor fields through real next requests', async () => {
  await compare('{"id":1}\n{"id":2}\n{"id":3}\n', [{ operation: 'page', limit: 1 }, { operation: 'append', bytes: hex('{"id":4}\n') }, { operation: 'page', cursorFrom: 0, modifyCursor: { future: { token: '\ud800', number: 9007199254740992 } } }, { operation: 'page' }, { operation: 'page', cursorFrom: 0, legacy: true }, { operation: 'rewrite', bytes: hex('{"new":1}\n{"new":2}\n{"new":3}\n{"new":4}\n{"new":5}\n') }, { operation: 'page', cursorFrom: 0 }, { operation: 'page' }])
}, 45_000)
test('cursor validation and limit errors match actual source status/code/message', async () => {
  const cursor = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('base64url')
  await compare('{"id":1}\n', [{ operation: 'page', limit: 0 }, { operation: 'page', limit: -1 }, { operation: 'page', cursor: 'broken' }, { operation: 'page', cursor: cursor(null) }, { operation: 'page', cursor: 'a'.repeat(2049) }, { operation: 'page' }, ...[{ direction: null }, { size: -1 }, { offset: 9007199254740992 }, { skipping: 0 }, { fingerprints: { prefix: 'X', tail: 'x', boundary: 'x' } }].map(modifyCursor => ({ operation: 'page', cursorFrom: 5, modifyCursor }))])
}, 45_000)
test('large semantic records own pages intact, malformed and over-budget records continue', async () => {
  const initial = '{"first":true}\n' + JSON.stringify({ id: 'large', future: 'x'.repeat(300 * 1024), image: { data: 'fixture-only' } }) + '\n' + JSON.stringify({ id: 'too-large', body: 'x'.repeat(8 * 1024 * 1024 + 1) }) + '\n{"last":true}\n'
  await compare(initial, [{ operation: 'page', limit: 1 }, { operation: 'page', cursorFrom: 0 }, { operation: 'page', cursorFrom: 1 }, { operation: 'page', full: true }, { operation: 'stream' }, { operation: 'stream', maxRecordBytes: 400 * 1024 }])
}, 45_000)
test('row budgets retain whole block arrays and full mode walks beyond a screenful', async () => {
  const initial = Array.from({ length: 210 }, (_, id) => JSON.stringify({ id, message: { content: Array.from({ length: 3 }, () => ({ type: 'text', text: 'same row' })) } })).join('\n') + '\n'
  await compare(initial, [{ operation: 'page' }, { operation: 'page', cursorFrom: 0 }, { operation: 'page', full: true }, { operation: 'page', limit: 1.9 }])
}, 45_000)
test('forward offsets, unterminated final records and consumer failures remain authoritative', async () => {
  await compare('{"a":1}\ninvalid\n{"b":2}', [{ operation: 'stream', startOffset: 8, endOffset: 20 }, { operation: 'stream', maxRecordBytes: 3 }, { operation: 'stream', failConsumer: true }, { operation: 'stream' }])
}, 45_000)
test('display previews preserve UTF16 slicing, structural identities, shared budgets and unknown fields', async () => {
  const raw = JSON.stringify({ unknown: '\ud800', content: 'a'.repeat(16 * 1024 - 1) + '😀tail', message: { future: true, content: [{ type: 'tool_use', id: 'i'.repeat(4096), input: { value: 'z'.repeat(20000), '\ud800': 'unit' } }, ...Array.from({ length: 300 }, (_, id) => ({ id, text: 'b'.repeat(1000) }))] }, toolUseResult: { body: 'c'.repeat(20000) }, bodyTruncated: false })
  await compare('', [{ operation: 'preview', raw }, ...['{}', '{"message":["😀"],"content":null}', '{"message":"😀"}', '{"message":42}', '{"message":1e400}', '{"message":-0}', '{"message":{"type":"unknown"}}'].map(raw => ({ operation: 'preview', raw }))])
}, 45_000)

test('partial UTF8 reads, invalid byte replacement and BOM JSON rejection match source offsets', async () => {
  const initial = Buffer.concat([Buffer.from('{"text":"😀'), Buffer.from([0xf0, 0x9f, 0x22, 0x7d, 0x0a]), Buffer.from('\ufeff{"bom":true}\n{"end":"\\udfff"}')])
  await compare(initial, [{ operation: 'page' }, { operation: 'stream' }], 3)
}, 45_000)
test('scan ceiling resumes an oversized line and only counts the omission on its first page', async () => {
  await compare('{"head":true}\n' + 'x'.repeat(16 * 1024 * 1024 + 65536) + '\n', [{ operation: 'page' }, { operation: 'page', cursorFrom: 0 }, { operation: 'stream' }])
}, 45_000)
test('actual concurrent append/rewrite/truncation retains failures and recovers at the next request', async () => {
  for (const mutation of [
    { index: 3, operation: 'append', bytes: hex('{"id":3}\n') },
    { index: 3, operation: 'rewrite', bytes: hex('{"id":4}\n{"id":5}\n') },
    { index: 3, operation: 'rewrite', bytes: hex('{"id":4}\n{"id":5}\n'), preserveTime: true },
    { index: 1, operation: 'rewrite', bytes: hex('{"id":4}\n') },
  ] as Mutation[]) await compare('{"id":1}\n{"id":2}\n', [{ operation: 'page' }, { operation: 'page' }, { operation: 'stream' }], undefined, mutation)
}, 45_000)
test('actual source four-lane admission keeps FIFO, abort removal, busy errors and slot recovery', async () => {
  await compare('', [{ operation: 'cancelled' }, ...['page', 'context', 'recovery', 'metadata'].map(raw => ({ operation: 'admission', raw }))])
}, 45_000)
test('full-history byte and scan ceilings match complete source serialization and continuation', async () => {
  const record = JSON.stringify({ future: 'x'.repeat(6 * 1024 * 1024) }) + '\n'
  await compare('{"head":true}\n' + record.repeat(6), [{ operation: 'page', full: true }, { operation: 'page', full: true, cursorFrom: 0 }], undefined, undefined, true)
  await compare('{"head":true}\n' + 'x'.repeat(64 * 1024 * 1024 + 65536) + '\n', [{ operation: 'page', full: true }, { operation: 'page', full: true, cursorFrom: 0 }], undefined, undefined, true)
}, 90_000)
test('close failure overrides successful reads and consumer errors, then the next request recovers', async () => {
  await compare('{"id":1}\n', [{ operation: 'page' }, { operation: 'page' }], undefined, undefined, undefined, 1)
  await compare('{"id":1}\n', [{ operation: 'stream', failConsumer: true }, { operation: 'stream' }], undefined, undefined, undefined, 1)
}, 45_000)

for (const kind of ['typescript', 'go'] as const) {
  test(`${kind} fixture rejects private transcript/output leaf and HOME/config directory symlinks without changing sentinels`, async () => {
    for (const boundary of ['transcript', 'output', 'home', 'config', 'dangling-transcript', 'dangling-output', 'dangling-config'] as const) {
      const parent = await mkdtemp(join(allocationRoot, 'session-reader-symlink-'))
      let child: ReturnType<typeof Bun.spawn> | undefined
      try {
        const actualHome = join(parent, 'home')
        const protectedHome = join(parent, 'protected')
        await mkdir(actualHome)
        await mkdir(protectedHome)
        const sentinel = join(protectedHome, 'sentinel.jsonl')
        const protectedBytes = Buffer.from('{"protected":"never read or mutate"}\n')
        await writeFile(sentinel, protectedBytes)
        const home = boundary === 'home' ? join(parent, 'home-alias') : actualHome
        if (boundary === 'home') await symlink(actualHome, home, process.platform === 'win32' ? 'junction' : 'dir')
        const config = join(home, '.claude')
        if (boundary === 'config' || boundary === 'dangling-config') await symlink(boundary === 'config' ? protectedHome : join(protectedHome, 'missing-config'), config, process.platform === 'win32' ? 'junction' : 'dir')
        else await mkdir(config)
        const transcript = join(home, 'session.jsonl')
        if (boundary === 'transcript' || boundary === 'dangling-transcript') await symlink(boundary === 'transcript' ? sentinel : join(protectedHome, 'missing-transcript'), transcript, 'file')
        else await writeFile(transcript, '{"fixture":true}\n')
        if (boundary === 'output' || boundary === 'dangling-output') await symlink(boundary === 'output' ? sentinel : join(protectedHome, 'missing-output'), join(home, 'reader-result.json'), 'file')
        child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/session-reader.fixture.ts')], {
          cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment),
          stdin: new Blob([JSON.stringify({ home, steps: [{ operation: 'rewrite', bytes: hex('must never write') }] })]), stdout: 'pipe', stderr: 'pipe',
        })
        const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
        try {
          const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          expect(code, `${kind}/${boundary}: ${stderr}`).not.toBe(0)
          expect(stdout).toBe('')
          expect(stderr).toContain('Session reader fixture')
          expect(await readFile(sentinel)).toEqual(protectedBytes)
          for (const missing of ['missing-config', 'missing-transcript', 'missing-output']) await expect(readFile(join(protectedHome, missing))).rejects.toHaveProperty('code', 'ENOENT')
        } finally { clearTimeout(timer) }
      } finally { child?.kill(); if (child) await child.exited; await rm(parent, { recursive: true, force: true }) }
    }
  }, 45_000)
  test(`${kind} fixture rejects inconsistent allocation, cwd and environment before private writes`, async () => {
    for (const boundary of ['allocation', 'allocation-self', 'cwd', 'profile', 'env-file', 'home', 'config'] as const) {
      const parent = await mkdtemp(join(allocationRoot, 'session-reader-boundary-'))
      let child: ReturnType<typeof Bun.spawn> | undefined
      try {
        const home = join(parent, 'home')
        await mkdir(home)
        await mkdir(join(home, '.claude'))
        const transcript = join(home, 'session.jsonl')
        const sentinelBytes = Buffer.from('{"protected":"unchanged"}\n')
        await writeFile(transcript, sentinelBytes)
        const overrides: Record<string, string> = boundary === 'allocation' ? { CC_HAHA_FIXTURE_TEMP_ROOT: '' }
          : boundary === 'allocation-self' ? { CC_HAHA_FIXTURE_TEMP_ROOT: home }
            : boundary === 'profile' ? { USERPROFILE: parent }
              : boundary === 'env-file' ? { BUN_OPTIONS: '' }
                : boundary === 'home' ? { HOME: parent }
                  : boundary === 'config' ? { CLAUDE_CONFIG_DIR: parent } : {}
        child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/session-reader.fixture.ts')], {
          cwd: boundary === 'cwd' ? parent : home, env: createSandboxedTestEnvironment(home, { ...fixtureEnvironment, ...overrides }),
          stdin: new Blob([JSON.stringify({ home, steps: [{ operation: 'rewrite', bytes: hex('must never write') }] })]), stdout: 'pipe', stderr: 'pipe',
        })
        const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
        try {
          const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          expect(code, `${kind}/${boundary}: ${stderr}`).not.toBe(0)
          expect(stdout).toBe('')
          expect(stderr).toContain('Session reader fixture requires disposable HOME/config')
          expect(await readFile(transcript)).toEqual(sentinelBytes)
          await expect(readFile(join(home, 'reader-result.json'))).rejects.toHaveProperty('code', 'ENOENT')
        } finally { clearTimeout(timer) }
      } finally { child?.kill(); if (child) await child.exited; await rm(parent, { recursive: true, force: true }) }
    }
  }, 45_000)
}
