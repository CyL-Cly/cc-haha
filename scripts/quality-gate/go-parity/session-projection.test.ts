import { afterAll, expect, test } from 'bun:test'
import { access, mkdir, mkdtemp, readFile, rm, stat, symlink, utimes, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
const id = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
const projectDir = '-fixture-project'
let buildHome: string | undefined
let executable: string
let build: Promise<void> | undefined
type Step = {
  operation: 'projection' | 'launch' | 'append' | 'rewrite' | 'restart' | 'concurrent'
  bytes?: string
  projectDir?: string
  memory?: Record<string, unknown>
  count?: number
}
function ensureGo() {
  return build ??= (async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'session-projection-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessionprojection/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
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
const hex = (value: string) => Buffer.from(value).toString('hex')
async function run(kind: 'typescript' | 'go', home: string, steps: Step[]) {
  if (kind === 'go') await ensureGo()
  const file = join(home, '.claude', 'projects', projectDir, id + '.jsonl')
  const info = await stat(file, { bigint: true })
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/session-projection.fixture.ts')], {
    cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, TZ: 'UTC' }),
    stdin: new Blob([JSON.stringify({ home, projectDir, steps, birthtimeISO: info.birthtime.toISOString() })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 45_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, kind + ': ' + stderr).toBe(0)
    expect(stderr).toBe('')
    expect(JSON.parse(stdout)).toEqual({ result: 'projection-result.json' })
    return JSON.parse(await readFile(join(home, 'projection-result.json'), 'utf8')) as { results: Array<{ bytes?: string, error?: unknown }>, trace: Array<{ operation: string }> }
  } finally {
    clearTimeout(timer)
    child.kill()
    await child.exited
  }
}
async function compare(initial: string, steps: Step[] = [{ operation: 'projection' }], sourceOnly = false, persistedBranch = false) {
  const home = await mkdtemp(join(allocationRoot, 'session-projection-case-'))
  try {
    const folder = join(home, '.claude', 'projects', projectDir)
    await mkdir(folder, { recursive: true })
    const file = join(folder, id + '.jsonl')
    const initialize = async () => {
      await writeFile(file, initial)
      await utimes(file, 1_700_000_000, 1_700_000_000)
    }
    await initialize()
    const source = await run('typescript', home, steps)
    const final = await readFile(file)
    await initialize()
    const concurrent = steps.some(step => step.operation === 'concurrent')
    const replay = await run('typescript', home, steps)
    const counts = (trace: typeof source.trace) => trace.reduce<Record<string, number>>((all, event) => {
      const key = JSON.stringify(event)
      all[key] = (all[key] ?? 0) + 1
      return all
    }, {})
    if (concurrent) {
      expect(replay.results).toEqual(source.results)
      expect(counts(replay.trace)).toEqual(counts(source.trace))
    } else {
      expect(replay).toEqual(source)
    }
    if (!sourceOnly) {
      await initialize()
      const actual = await run('go', home, steps)
      if (concurrent) {
        // Concurrent native stat completions have no Source total order.
        // Keep both raw traces while asserting identical results, every
        // operation/offset/length/count, and exactly one actual shared scan.
        expect(actual.results).toEqual(source.results)
        expect(counts(actual.trace)).toEqual(counts(source.trace))
        expect(actual.trace.filter(event => event.operation === 'open')).toHaveLength(1)
        const artifacts = join(root, 'artifacts/go-parity/session-projection')
        await mkdir(artifacts, { recursive: true })
        await writeFile(join(artifacts, 'concurrent-native-traces.json'), JSON.stringify({ source: source.trace, replay: replay.trace, go: actual.trace }, null, 2))
      } else if (persistedBranch) {
        // Source also runs its locator and transcript-existence scan. The Go
        // component starts after lookup; retain those caller traces as evidence
        // and compare the persisted result and actual write effects here.
        expect(actual.results).toEqual(source.results)
        expect(source.trace.length).toBeGreaterThan(actual.trace.length)
        const artifacts = join(root, 'artifacts/go-parity/session-projection')
        await mkdir(artifacts, { recursive: true })
        await writeFile(join(artifacts, 'persisted-caller-traces.json'), JSON.stringify({ scope: 'persisted launch result; Go locator not migrated', source: source.trace, go: actual.trace }, null, 2))
      } else {
        expect(actual).toEqual(source)
      }
      expect(await readFile(file)).toEqual(final)
    }
    return source
  } finally {
    await rm(home, { recursive: true, force: true })
  }
}
test('actual Source projection self replay keeps first/cache/EOF distinctions', async () => {
  const source = await compare('{"type":"user","timestamp":"2025-01-02T00:00:00Z","message":{"role":"user","content":"First"}}\n{"type":"custom-title","customTitle":"EOF"}', [{ operation: 'projection' }, { operation: 'projection' }], true)
  const first = JSON.parse(Buffer.from(source.results[0]!.bytes!, 'hex').toString())
  const cached = JSON.parse(Buffer.from(source.results[1]!.bytes!, 'hex').toString())
  expect(first.summary.title).toBe('First')
  expect(first.launchInfo.customTitle).toBe('EOF')
  expect(first.signature).toBeUndefined()
  expect(cached.signature).toBeString()
}, 90_000)
test('ordinary projection preserves complete vs EOF counts, first titles and null/runtime replacement', async () => {
  await compare([
    { type: 'session-meta', workDir: '', runtimeProviderId: 'old', runtimeModelId: 'old', effortLevel: 'high', permissionMode: 'plan' },
    { type: 'user', cwd: 'C:', timestamp: '2025-01-02T00:00:00Z', message: { role: 'user', content: 'First' } },
    { type: 'user', isMeta: true, timestamp: '2027-01-02T00:00:00Z', message: { role: 'user', content: 'Hidden' } },
    { type: 'session-meta', runtimeProviderId: null, runtimeModelId: 'new', permissionMode: 'bogus' },
    { type: 'custom-title', customTitle: 'Title' },
    { type: 'custom-title', customTitle: '' },
  ].map(value => JSON.stringify(value)).join('\n'), [{ operation: 'projection' }, { operation: 'projection' }])
}, 90_000)
test('title precedence, XML command sources, whitespace and UTF16 truncation match Source', async () => {
  for (const rows of [
    [{ type: 'user', message: { role: 'user', content: '<system-reminder>hidden</system-reminder>\nhello\u00a0\u2028world' } }, { type: 'ai-title', aiTitle: '<command-name attr="yes">/ask</command-name><command-args>a &amp; b</command-args>' }],
    [{ type: 'user', message: { role: 'user', content: [{ type: 'image', source: {} }, { type: 'text', text: 'x'.repeat(79) + '😀tail' }] } }],
    [{ type: 'system', subtype: 'local_command', content: '<command-name>/goal</command-name><command-args>clear now</command-args>' }, { type: 'system', subtype: 'local_command', content: '<COMMAND-NAME>/goal</COMMAND-NAME><command-args>first &lt;goal&gt;</command-args>' }, { type: 'system', subtype: 'local_command', content: '<command-name>/goal</command-name><command-args>later</command-args>' }, { type: 'custom-title', customTitle: ' ' }],
    [{ type: 'ai-title', aiTitle: [1, null, 'future'] }, { type: 'ai-title', aiTitle: '<hidden></hidden>' }],
    [{ type: 'user', message: { role: 'user', content: '<Missing>keep</Missing> <x a="1">drop</x>\n<x>drop</x>' } }],
    [{ type: 'user', message: { role: 'user', content: '<command-name-long>drop</command-name-long>keep' } }],
  ]) await compare(rows.map(value => JSON.stringify(value)).join('\n') + '\n')
}, 90_000)
test('unknown repository/worktree shapes, nested ordering and serialized numbers keep Source strategy', async () => {
  await compare('{"type":"worktree-state","worktreeSession":{"worktreePath":"","worktreeName":"","future":{"2":2,"1":1,"n":1e400,"unit":"\\ud800"}}}\n{"repository":{"__proto__":{"own":true},"large":9007199254740993,"order":1,"order":2}}\n{"type":"system","message":{"role":"system"},"entrypoint":"claude-desktop-team-worker"}\n{"type":"worktree-state","worktreeSession":null}\n{"repository":[]}\n')
  await compare('{"type":"assistant","timestamp":1e400,"message":{"role":true}}\n{"type":"assistant","timestamp":"2025-01-01T00:00:00Z","message":{"role":"assistant"}}\n')
}, 90_000)
test('declared ISO/RFC dates keep invalid/offset/rollover and equal timestamp priority', async () => {
  const stamps = ['2024-02-29T00:00:00Z', '2025-02-29T01:00:00.123999Z', '2025-02-29T02:00:00.123+01:00', '2025-01-01T24:00:00Z', '2025-13-01T00:00:00Z', '2025-01-01T25:00:00Z', '2025-01-01T00:00:00+24:00', '2025-01-01T00:00:60Z', 'Wed, 31 Dec 2025 23:00:00 GMT', 'Thu, 01 Jan 2026 00:30:00 +0100', '2026', '2026-02', '2026-02-03']
  await compare(stamps.map(timestamp => JSON.stringify({ type: 'assistant', timestamp, message: { role: 'assistant', content: 'reply' } })).join('\n') + '\n')
  for (const timestamp of stamps) await compare(JSON.stringify({ type: 'assistant', timestamp, message: { role: 'assistant' } }) + '\n')
}, 90_000)
test('actual append/rewrite/restart updates metadata cache and error retries do not retain failed projections', async () => {
  await compare('{"type":"user","message":{"role":"user","content":"Original"}}\n', [
    { operation: 'projection' }, { operation: 'projection', projectDir: 'C--Users-name' },
    { operation: 'append', bytes: hex('{"type":"custom-title","customTitle":"Appended"}\n') },
    { operation: 'projection' }, { operation: 'restart' }, { operation: 'projection' },
    { operation: 'rewrite', bytes: hex('{"type":"custom-title","customTitle":"Rewritten"}\n') }, { operation: 'projection' },
  ])
  await compare(JSON.stringify({ type: 'session-meta', workDir: 'a'.repeat(128 * 1024) }) + '\n', [{ operation: 'projection' }, { operation: 'projection' }, { operation: 'rewrite', bytes: hex('{"type":"custom-title","customTitle":"Recovered"}\n') }, { operation: 'projection' }])
}, 90_000)
test('path fallbacks use logical project desanitization while explicit empty workDir uses actual cwd', async () => {
  for (const project of ['', 'C--', 'C--Users-name', '--server-share', '-a-b', '-a-\u0007-\u000b']) await compare('', [{ operation: 'projection', projectDir: project }])
}, 90_000)
test('oversized image and sole legacy Agent turns remain resumable with latest runtime and worktree clearing', async () => {
  const body = 'x'.repeat(9 * 1024 * 1024)
  await compare([
    { type: 'session-meta', runtimeProviderId: 'old', runtimeModelId: 'old', effortLevel: 'high' },
    { type: 'user', message: { role: 'user', content: [{ type: 'image', source: { data: body } }, { type: 'text', text: 'Image task' }] } },
    { type: 'session-meta', runtimeProviderId: 'new', runtimeModelId: 'new' },
  ].map(value => JSON.stringify(value)).join('\n') + '\n', [{ operation: 'projection' }, { operation: 'restart' }, { operation: 'projection' }])
  await compare(JSON.stringify({ type: 'assistant', cwd: 'C:', message: { role: 'assistant', content: [{ type: 'tool_use', name: 'Agent', input: { prompt: body } }] }, repository: { repoName: 'fixture', future: true } }) + '\n')
}, 90_000)
test('actual Source null block TypeError and failed-build retry keep the same error priority', async () => {
  await compare('{"type":"user","message":{"role":"user","content":[null]}}\n', [{ operation: 'projection' }, { operation: 'projection' }, { operation: 'rewrite', bytes: hex('{"type":"user","message":{"role":"user","content":"retry"}}\n') }, { operation: 'projection' }])
}, 90_000)
test('persisted public Source launch value retains memory unknowns but restores actual transcript count', async () => {
  await compare('{"type":"user","message":{"role":"user","content":"body"}}\n', [{ operation: 'launch', memory: { workDir: '/fixture/private', customTitle: 'memory', transcriptMessageCount: 999, future: { intact: true } } }, { operation: 'launch' }], false, true)
}, 90_000)
test('concurrent actual Source and Go callers share one oversized metadata build and reuse its cache', async () => {
  await compare(JSON.stringify({ type: 'assistant', message: { role: 'assistant', content: [{ type: 'image', source: { data: 'x'.repeat(9 * 1024 * 1024) } }] } }) + '\n', [{ operation: 'concurrent', count: 8 }, { operation: 'projection' }])
}, 90_000)
for (const kind of ['typescript', 'go'] as const) {
  test(kind + ' projection fixture refuses leaf and directory symlinks before touching sentinels', async () => {
    if (kind === 'go') await ensureGo()
    for (const boundary of ['input', 'output', 'home', 'config', 'project', 'missing-input'] as const) {
      const parent = await mkdtemp(join(allocationRoot, 'session-projection-reject-'))
      let child: ReturnType<typeof Bun.spawn> | undefined
      try {
        const actualHome = join(parent, 'home')
        const protectedHome = join(parent, 'protected')
        await mkdir(actualHome)
        await mkdir(protectedHome)
        const sentinel = join(protectedHome, boundary === 'missing-input' ? 'sentinel' : id + '.jsonl')
        const bytes = Buffer.from('protected unchanged')
        await writeFile(sentinel, bytes)
        const home = boundary === 'home' ? join(parent, 'alias') : actualHome
        if (boundary === 'home') await symlink(actualHome, home, process.platform === 'win32' ? 'junction' : 'dir')
        if (boundary === 'config') await symlink(protectedHome, join(home, '.claude'), process.platform === 'win32' ? 'junction' : 'dir')
        else {
          const projects = join(home, '.claude', 'projects')
          await mkdir(projects, { recursive: true })
          const folder = join(projects, projectDir)
          if (boundary === 'project' || boundary === 'missing-input') await symlink(protectedHome, folder, process.platform === 'win32' ? 'junction' : 'dir')
          else {
            await mkdir(folder)
            if (boundary === 'input') await symlink(sentinel, join(folder, id + '.jsonl'), 'file')
            else await writeFile(join(folder, id + '.jsonl'), '{}')
          }
        }
        if (boundary === 'output') await symlink(sentinel, join(home, 'projection-result.json'), 'file')
        child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/session-projection.fixture.ts')], {
          cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
          stdin: new Blob([JSON.stringify({ home, projectDir, steps: [{ operation: 'rewrite', bytes: hex('must never write') }] })]), stdout: 'pipe', stderr: 'pipe',
        })
        const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
        try {
          const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          expect(code).not.toBe(0)
          expect(stdout).toBe('')
          expect(stderr).toContain('Session projection fixture')
          expect(await readFile(sentinel)).toEqual(bytes)
          if (boundary === 'missing-input') expect(await access(join(protectedHome, id + '.jsonl')).then(() => true, () => false)).toBe(false)
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
