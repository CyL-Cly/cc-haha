import { afterAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, utimes, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let buildHome: string | undefined
let executable: string
let build: Promise<void> | undefined
function ensureGo() {
  return build ??= (async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'session-metadata-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessionmetadata/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try {
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
      expect(code, stderr).toBe(0)
    } finally {
      clearTimeout(timer)
    }
  })()
}
afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
})
type Step = {
  operation: 'stream' | 'append' | 'rewrite'
  bytes?: string
  startOffset?: number
  endOffset?: number
  failConsumer?: boolean
  failSkipped?: boolean
  aborted?: boolean
  cancelAfterRead?: number
}
type Fixture = {
  maxRead?: number
  failCloseCount?: number
  mutateOnRead?: {
    index: number
    operation: 'append' | 'rewrite'
    bytes: string
    preserveTime?: boolean
  }
}
const large = 'x'.repeat(8 * 1024 * 1024 + 1)
const hex = (value: string) => Buffer.from(value).toString('hex')
async function run(kind: 'typescript' | 'go', home: string, steps: Step[], options: Fixture = {}) {
  if (kind === 'go') await ensureGo()
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/session-metadata.fixture.ts')], {
    cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
    stdin: new Blob([JSON.stringify({ home, steps, ...options })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 45_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, kind + ': ' + stderr).toBe(0)
    expect(stderr).toBe('')
    expect(JSON.parse(stdout)).toEqual({ result: 'metadata-result.json' })
    return JSON.parse(await readFile(join(home, 'metadata-result.json'), 'utf8')) as {
      results: Array<{ bytes?: string, error?: unknown }>
      trace: unknown[]
    }
  } finally {
    clearTimeout(timer)
    child.kill()
    await child.exited
  }
}
async function compare(initial: string | Buffer, steps: Step[] = [{ operation: 'stream' }], options: Fixture = {}, sourceOnly = false) {
  const home = await mkdtemp(join(allocationRoot, 'session-metadata-case-'))
  try {
    await mkdir(join(home, '.claude'))
    const file = join(home, 'metadata.jsonl')
    const initialize = async () => {
      await writeFile(file, initial)
      await utimes(file, 1_700_000_000, 1_700_000_000)
    }
    await initialize()
    const source = await run('typescript', home, steps, options)
    const final = await readFile(file)
    await initialize()
    expect(await run('typescript', home, steps, options)).toEqual(source)
    if (!sourceOnly) {
      await initialize()
      expect(await run('go', home, steps, options)).toEqual(source)
      expect(await readFile(file)).toEqual(final)
    }
    return source
  } finally {
    await rm(home, { recursive: true, force: true })
  }
}
test('actual source metadata self replay retains valid envelopes and rejects malformed discarded payloads', async () => {
  const value = await compare('{"unused":"' + large + '","cwd":"/kept","message":{"content":"ok"}}\n' + '{"unused":"' + large + '","workDir":"/invalid",}\n', undefined, {}, true)
  const result = JSON.parse(Buffer.from(value.results[0]!.bytes!, 'hex').toString())
  expect(result.scan).toMatchObject({ omittedRecords: 1, projectedRecords: 1 })
  expect(result.entries[0].entry).toEqual({ cwd: '/kept', message: { content: 'ok' } })
}, 90_000)
test('ordinary complete entries and valid scalar lines preserve unknowns, duplicate keys, UTF16 and offsets', async () => {
  await compare('{"2":2,"1":1,"unknown":9007199254740993,"x":1,"x":2,"text":"\\ud800","message":{"content":"' + 'z'.repeat(70_000) + '"}}\r\n\ninvalid\nfalse\n[]\nnull\n{"tail":true}')
}, 90_000)
test('the exact 8 MiB record boundary retains the ordinary body and switches only after one more byte', async () => {
  const prefix = '{"message":{"content":"'
  const suffix = '"}}'
  for (const additional of [0, 1]) {
    const raw = prefix + 'x'.repeat(8 * 1024 * 1024 - prefix.length - suffix.length + additional) + suffix
    const source = await compare(raw + '\n')
    const value = JSON.parse(Buffer.from(source.results[0]!.bytes!, 'hex').toString())
    expect(value.scan.projectedRecords).toBe(additional)
    expect(value.entries[0].truncated).toBe(additional === 1)
    expect(value.entries[0].entry.message.content.length).toBe(additional ? 4096 : 8 * 1024 * 1024 - prefix.length - suffix.length)
  }
}, 90_000)
test('oversized images/tools discard bodies and retain every Agent identity and metadata after the body', async () => {
  await compare(JSON.stringify({ type: 'assistant', message: { content: [{ type: 'image', source: { data: large } }, { type: 'tool_use', input: { prompt: large }, id: 'agent', name: 'Agent' }, { type: 'tool_result', tool_use_id: 'id', content: large }, { type: 'text', text: '你好 🌎' }], role: 'assistant', unknown: 'discarded' }, cwd: '/正确', uuid: 'u', repository: { future: true, __proto__: null }, unknown: true }) + '\n')
}, 90_000)
test('preview slices UTF16 units and keeps truncation flags only for surviving message slots', async () => {
  for (const value of [
    '{"unused":"' + large + '","content":"' + 'r'.repeat(5000) + '","message":{"role":"user","content":"' + 'x'.repeat(4095) + '😀tail"}}',
    '{"message":{"content":"' + large + '"},"message":{"role":"user","content":"final"}}',
    '{"message":{"content":"' + large + '","content":"final"}}',
    '{"message":{"content":[{"type":"text","text":"' + large + '","text":"final"}]}}',
  ]) await compare(value)
}, 90_000)
test('all-byte validation drops invalid escapes, numbers, tails and malformed oversized JSON then resumes', async () => {
  await compare(['{"unused":"' + large + '\\q","workDir":"/wrong"}', '{"unused":"' + large + '","repository":{"bad":01}}', '{"unused":"' + large + '",}', '{"unused":"' + large + '"}trailing', '{"unused":"' + large, '{"workDir":"/next"}'].join('\n'))
}, 90_000)
test('retained metadata bounds, key limits and all-subtree depth limits propagate explicit errors', async () => {
  for (const value of [
    JSON.stringify({ unused: large, workDir: 'x'.repeat(128 * 1024 + 1) }),
    JSON.stringify({ unused: large, repository: { ['k'.repeat(257)]: 1 } }),
    '{"unused":"' + large + '","discarded":' + '['.repeat(130) + '0' + ']'.repeat(130) + '}',
    JSON.stringify({ ['k'.repeat(8 * 1024 * 1024 + 1)]: 1, cwd: '/known' }),
    '{"unused":"' + large + '","repository":{"__proto__":{"polluted":true},"2":2,"1":1,"value":1e400},"message":null}',
  ]) await compare(value, [{ operation: 'stream' }, { operation: 'rewrite', bytes: hex('{"cwd":"/retry"}\n') }, { operation: 'stream' }])
}, 90_000)
test('byte ranges, partial reads and invalid UTF8 replacement match full trace and final-line boundaries', async () => {
  const prefix = '{"before":true}\n'
  const body = JSON.stringify({ type: 'session-meta', unused: large, cwd: '/范围' }) + '\n'
  await compare(prefix + body + '{"after":true}', [{ operation: 'stream', startOffset: Buffer.byteLength(prefix), endOffset: Buffer.byteLength(prefix + body) }], { maxRead: 17001 })
  await compare(Buffer.concat([Buffer.from('{"unused":"' + large + '","message":{"content":"'), Buffer.from([0xf0, 0x9f, 0x22]), Buffer.from('},"cwd":"/tail"}')]), undefined, { maxRead: 65533 })
}, 90_000)
test('append and rewrite during actual reads preserve error/next-request behavior and complete EOF offsets', async () => {
  for (const mutation of [
    { index: 2, operation: 'append', bytes: hex('{"new":true}\n') },
    { index: 2, operation: 'rewrite', bytes: hex('{"changed":true}\n') },
  ] as const) await compare(JSON.stringify({ unused: large, cwd: '/initial' }) + '\n', [{ operation: 'stream' }, { operation: 'stream' }], { mutateOnRead: mutation })
  await compare('{"last":true}', [{ operation: 'stream' }, { operation: 'append', bytes: hex('\n{"next":true}\n') }, { operation: 'stream', startOffset: 0 }])
}, 90_000)
test('consumer/cancellation/finally-close failures stay failures and following reads recover', async () => {
  await compare('invalid\n{"last":true}\n', [{ operation: 'stream', failSkipped: true }, { operation: 'stream' }])
  await compare('{"first":true}\n', [{ operation: 'stream', failConsumer: true }, { operation: 'stream', aborted: true }, { operation: 'stream', cancelAfterRead: 3 }, { operation: 'stream' }])
  await compare('', [{ operation: 'stream', aborted: true }])
  await compare('{"first":true}\n', [{ operation: 'stream', failConsumer: true }, { operation: 'stream' }], { failCloseCount: 1 })
}, 90_000)
test('streamed scalar grammar and UTF16 key byte charges match actual source values', async () => {
  const values = ['-0', '-1.25e+4', '1E-5', '1e400', 'true', 'false', 'null', '"\\ud800\\udfff😀"']
  await compare(values.map(value => '{"unused":"' + large + '","repository":{"value":' + value + ',"\\ud800":"key","😀":"pair"}}').join('\n'))
  await compare('{"unused":"' + large + '","repository":{"value":"' + 'a'.repeat(128 * 1024 - 10) + '"},}\n{"cwd":"/retry"}')
}, 90_000)
for (const kind of ['typescript', 'go'] as const) {
  test(kind + ' metadata fixture refuses private leaf and directory symlinks before touching sentinels', async () => {
    if (kind === 'go') await ensureGo()
    for (const boundary of ['input', 'output', 'home', 'config'] as const) {
      const parent = await mkdtemp(join(allocationRoot, 'session-metadata-reject-'))
      let child: ReturnType<typeof Bun.spawn> | undefined
      try {
        const actualHome = join(parent, 'home')
        const protectedHome = join(parent, 'protected')
        await mkdir(actualHome)
        await mkdir(protectedHome)
        const sentinel = join(protectedHome, 'sentinel')
        const bytes = Buffer.from('protected unchanged')
        await writeFile(sentinel, bytes)
        const home = boundary === 'home' ? join(parent, 'alias') : actualHome
        if (boundary === 'home') await symlink(actualHome, home, process.platform === 'win32' ? 'junction' : 'dir')
        if (boundary === 'config') await symlink(protectedHome, join(home, '.claude'), process.platform === 'win32' ? 'junction' : 'dir')
        else await mkdir(join(home, '.claude'))
        if (boundary === 'input') await symlink(sentinel, join(home, 'metadata.jsonl'), 'file')
        else await writeFile(join(home, 'metadata.jsonl'), '{}')
        if (boundary === 'output') await symlink(sentinel, join(home, 'metadata-result.json'), 'file')
        child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/session-metadata.fixture.ts')], {
          cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
          stdin: new Blob([JSON.stringify({ home, steps: [{ operation: 'rewrite', bytes: hex('must never write') }] })]), stdout: 'pipe', stderr: 'pipe',
        })
        const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
        try {
          const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          expect(code).not.toBe(0)
          expect(stdout).toBe('')
          expect(stderr).toContain('Session metadata fixture')
          expect(await readFile(sentinel)).toEqual(bytes)
        } finally {
          clearTimeout(timer)
        }
      } finally {
        child?.kill()
        if (child) await child.exited
        await rm(parent, { recursive: true, force: true })
      }
    }
  }, 45_000)
}
