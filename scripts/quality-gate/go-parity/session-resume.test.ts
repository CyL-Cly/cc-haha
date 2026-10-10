import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { realpathSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { resumeCases } from './session-resume-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir()
const artifact = join(root, 'artifacts/go-parity/session-resume')
let buildHome: string, executable: string
const contracts: unknown[] = []
beforeAll(async () => {
  buildHome = await mkdtemp(join(allocationRoot, 'session-resume-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessionresume/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
  if (contracts.length) { await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, 'source-contract.json'), JSON.stringify({ source: 'Bun1.3.14 actual loadMessagesFromJsonlPath + deserializeMessagesWithInterruptDetection', contracts }, null, 2) + '\n') }
})
type Step = { operation: 'reduce' | 'path' | 'rewrite' | 'append', sources?: string[], bytes?: string }
async function run(home: string, steps: Step[], go = false, classifier = false) {
  const command = go ? [executable] : [process.execPath, '--no-env-file', ...(classifier ? ['--feature=TRANSCRIPT_CLASSIFIER'] : []), join(root, 'scripts/quality-gate/go-parity/session-resume.fixture.ts')]
  const child = Bun.spawn(command, { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_RESUME_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, TZ: 'UTC', NODE_ENV: 'production' }), stdin: new Blob([JSON.stringify({ home, classifier, steps })]), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 30_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0); expect(stderr).toBe(''); expect(JSON.parse(stdout)).toEqual({ result: 'resume-result.json' })
    const value = JSON.parse(await readFile(join(home, 'resume-result.json'), 'utf8'))
    if (!go) { expect(value.sourceOnly.capabilities.blocked).toEqual([]); expect(value.sourceOnly.capabilities.secureReads).toEqual([]); expect(value.sourceOnly.capabilities.absentReads).toEqual([]) }
    if (value.sourceOnly.rawErrors.length) { await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, (go ? 'go' : 'source') + '-raw-errors-' + contracts.length + '.json'), JSON.stringify(value.sourceOnly.rawErrors, null, 2)) }
    return value.observations
  } finally { clearTimeout(timer); child.kill(); await child.exited }
}
async function compare(name: string, create: (home: string) => Step[], options: { bytes?: string, classifier?: boolean } = {}) {
  const home = await mkdtemp(join(allocationRoot, 'session-resume-case-'))
  try {
    const steps = create(home)
    const initialize = async () => { if (options.bytes !== undefined) await writeFile(join(home, 'session.jsonl'), Buffer.from(options.bytes, 'hex')) }
    await initialize(); const source = await run(home, steps, false, options.classifier)
    const final = options.bytes !== undefined ? await readFile(join(home, 'session.jsonl')) : undefined
    await initialize(); expect(await run(home, steps, false, options.classifier)).toEqual(source)
    await initialize(); expect(await run(home, steps, true, options.classifier)).toEqual(source)
    if (final) expect(await readFile(join(home, 'session.jsonl'))).toEqual(final)
    contracts.push({ name, cwd: home, processCwd: realpathSync(home), classifier: options.classifier ?? false, steps, initialBytes: options.bytes, expected: source })
    return source
  } finally { await rm(home, { recursive: true, force: true }) }
}
for (const row of resumeCases('/fixture')) test('actual Source reducer: ' + row.name, () => compare(row.name, home => [{ operation: 'reduce', sources: resumeCases(home).find(value => value.name === row.name)!.sources }]), 90_000)
test('actual build permission feature accepts auto and keeps bubble invalid', () => compare('permission-classifier-build', home => [{ operation: 'reduce', sources: resumeCases(home).find(row => row.name === 'all-runtime-permissions-with-features-off')!.sources }], { classifier: true }), 90_000)

const user = (uuid: string, content: unknown, extra: Record<string, unknown> = {}) => ({ type: 'user', uuid, timestamp: '2025-01-01T00:00:00Z', message: { role: 'user', content }, ...extra })
const assistant = (uuid: string, content: unknown, extra: Record<string, unknown> = {}) => ({ type: 'assistant', uuid, timestamp: '2025-01-01T00:00:01Z', message: { id: uuid, role: 'assistant', content }, ...extra })
const reduce = (messages: unknown[]): Step[] => [{ operation: 'reduce', sources: messages.map(value => JSON.stringify(value)) }]
for (const id of [null, 0, -0, true, false, 'same', '\ud800', '\ufffd', {}, []]) test('actual scalar/object/missing ID equality: ' + JSON.stringify(id), () => compare('id-' + JSON.stringify(id), () => reduce([assistant('a', [{ type: 'tool_use', id, name: 'Read' }]), user('u', [{ type: 'tool_result', tool_use_id: id, content: '' }])])), 90_000)
for (const id of ['9007199254740993', '1e400', '-1e400', '1e-400']) test('actual JSON.parse numeric ID profile: ' + id, () => compare('numeric-' + id, () => [{ operation: 'reduce', sources: [JSON.stringify(assistant('a', [{ type: 'tool_use', id: 1, name: 'Read' }])).replace('"id":1', '"id":' + id), JSON.stringify(user('u', [{ type: 'tool_result', tool_use_id: 1, content: '' }])).replace('"tool_use_id":1', '"tool_use_id":' + id)] }]), 90_000)
test('actual first-user merge preserves ordered unknown envelope/API/block fields and hoists tools', () => compare('ordered-merge-future-fields', () => reduce([
  user('u1', [{ type: 'text', future: { z: 1, a: 2 }, text: 'one' }], { futureFirst: { z: true, a: '\ud800' }, isMeta: true }),
  assistant('a', [{ type: 'text', text: '' }]),
  user('u2', [{ type: 'text', text: 'two' }, { future: 1, type: 'tool_result', tool_use_id: 'orphan', content: 'c' }], { futureSecond: 4 }),
])), 90_000)
test('actual malformed primitive blocks and unknown messages are not globally schema repaired', () => compare('primitive-messages-blocks', () => reduce([1, false, 'record', [], { type: 'future', future: true }, assistant('a', [3, false, 'block']), user('u', [])])), 90_000)
test('actual attachment path handling retains raw filename UTF16 and converts display paths, relative paths, null display and priority', () => compare('path-unicode-profile', home => reduce([
  { type: 'attachment', attachment: { type: 'new_file', filename: 'relative/../a\ud800', future: 1 } },
  { type: 'attachment', attachment: { type: 'new_directory', path: home + '//a/./b/../😀' } },
  { type: 'attachment', attachment: { type: 'future', filename: null, path: home + '/ignored' } },
  { type: 'attachment', attachment: { type: 'future', path: home, displayPath: false } },
])), 90_000)

for (const content of [undefined, null, 3, false, {}]) test('actual invalid user merge content: ' + JSON.stringify(content), () => compare('merge-content-' + JSON.stringify(content), () => reduce([user('u1', content), assistant('a', [{ type: 'text', text: '' }]), user('u2', 'next')])), 90_000)
for (const content of [undefined, null, 3, false, {}, { at: null }, { at: 3 }]) test('actual invalid right user merge content: ' + JSON.stringify(content), () => compare('merge-right-content-' + JSON.stringify(content), () => reduce([user('u1', 'before'), assistant('a', [{ type: 'text', text: '' }]), user('u2', content)])), 90_000)
for (const content of [{ z: 1 }, { z: 'x', a: [1, null] }]) test('actual nonempty terminal assistant iterator: ' + JSON.stringify(content), () => compare('terminal-object-iterator-' + JSON.stringify(content), () => reduce([assistant('a', content), user('u', [{ type: 'tool_result', tool_use_id: 't', content: '' }])])), 90_000)
for (const content of [null, 3, false, {}]) test('actual invalid terminal assistant iterator: ' + JSON.stringify(content), () => compare('terminal-iterator-' + JSON.stringify(content), () => reduce([assistant('a', content), user('u', [{ type: 'tool_result', tool_use_id: 't', content: '' }])])), 90_000)
for (const field of [undefined, null, 3, false, [], {}, { trim: null }, { trim: 3 }]) test('actual text seam coercion: ' + JSON.stringify(field), () => compare('text-seam-' + JSON.stringify(field), () => reduce([user('u1', [{ type: 'text', text: field }]), assistant('a', [{ type: 'text', text: '' }]), user('u2', 'next')])), 90_000)
for (const field of [false, [], {}, { trim: null }, { trim: 3 }]) test('actual invalid assistant text trim: ' + JSON.stringify(field), () => compare('assistant-trim-' + JSON.stringify(field), () => reduce([assistant('a', [{ type: 'text', text: field }])])), 90_000)

test('actual numeric truthiness and unknown trailing record', () => compare('numeric-truthiness-unknown-last', () => [{ operation: 'reduce', sources: reduce([user('u', 'prompt', { toolUseResult: 1 })])[0]!.sources }, { operation: 'reduce', sources: reduce([{ type: 'future', unknown: true }])[0]!.sources }]), 90_000)
test('actual terminal search crosses noise and string assistant content', () => compare('terminal-search-noise-string', () => reduce([assistant('a1', [{ type: 'tool_use', id: 't', name: 'Read' }]), { type: 'progress' }, assistant('a2', 'text'), { type: 'system' }, user('u', [{ type: 'tool_result', tool_use_id: 't', content: '' }])])), 90_000)
test('actual context attachment requires array payload', () => compare('hook-context-nonarray', () => reduce([{ type: 'attachment', attachment: { type: 'hook_additional_context', content: 'invalid' } }])), 90_000)
test('actual invoked skills nonarray payload drops', () => compare('invoked-skills-nonarray', () => reduce([{ type: 'attachment', attachment: { type: 'invoked_skills', skills: 3 } }])), 90_000)
for (const trim of ['fn', false, { z: 1 }, ['x'], 'fn\ud800', 'line\nquote"slash\\']) test('actual custom noncallable trim renderer: ' + JSON.stringify(trim), () => compare('native-trim-render-' + JSON.stringify(trim), () => reduce([assistant('a', [{ type: 'text', text: { trim } }])])), 90_000)
const sid = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
const line = (value: unknown) => JSON.stringify(value) + '\n'
const record = (uuid: string, parentUuid: string | null, type: 'user' | 'assistant', content: unknown, extra: Record<string, unknown> = {}) => ({ parentUuid, ... (type === 'user' ? user(uuid, content) : assistant(uuid, content)), sessionId: sid, ...extra })
test('actual independent JSONL load → reducer includes append, rewrite, EOF and compact parent recovery', async () => {
  const initial = line(record('u1', null, 'user', 'first'))
  const source = await compare('jsonl-next-request-recovery', () => [
    { operation: 'path' }, { operation: 'append', bytes: Buffer.from(line(record('a1', 'u1', 'assistant', [{ type: 'text', text: 'done' }]))).toString('hex') }, { operation: 'path' },
    { operation: 'rewrite', bytes: Buffer.from(line({ type: 'system', subtype: 'compact_boundary', uuid: 'c', timestamp: '2025-01-01T00:00:00Z' }) + line(record('u2', 'c', 'user', 'new')).trimEnd()).toString('hex') }, { operation: 'path' },
  ], { bytes: Buffer.from(initial).toString('hex') })
  expect(JSON.parse(source[0].resultSource).turnInterruptionState.kind).toBe('interrupted_prompt')
  expect(JSON.parse(source[2].resultSource).turnInterruptionState.kind).toBe('none')
  expect(JSON.parse(source[4].resultSource).messages.some((value: any) => value.uuid === 'u1')).toBe(false)
}, 90_000)
test('actual legacy malformed lines and dangling tool fork choose the independent newest main chain', () => compare('jsonl-legacy-fork-tool', () => [{ operation: 'path' }], { bytes: Buffer.from(line(record('u1', null, 'user', 'first')) + '{bad}\n' + line(record('a1', 'u1', 'assistant', [{ type: 'tool_use', id: 't', name: 'Read', input: {} }])) + line(record('side', null, 'user', 'ignored', { isSidechain: true, timestamp: '2026-01-01T00:00:00Z' }))).toString('hex') }), 90_000)
test('strict result mutant detects messages, property order, interruption and generation drift', async () => {
  const observation = await compare('mutant-base', () => reduce([user('u', 'prompt')]))
  for (const change of [ (value: any) => { value[0].resultSource += ' ' }, (value: any) => { value[0].references.reverse() }, (value: any) => { value[0].allocations.reverse() }, (value: any) => { value[0].interruptionIndex = 3 } ]) {
    const mutated = structuredClone(observation); change(mutated); expect(mutated).not.toEqual(observation)
  }
}, 90_000)
for (const go of [false, true]) for (const directory of [false, true]) for (const target of ['session.jsonl', 'resume-result.json']) test(`${go ? 'Go' : 'Source'} denies ${directory ? 'chain' : 'leaf'} symlink ${target} without sentinel mutation`, async () => {
  const home = await mkdtemp(join(allocationRoot, 'session-resume-guard-')), outside = await mkdtemp(join(allocationRoot, 'session-resume-sentinel-'))
  const sentinel = join(outside, 'sentinel'), original = 'protected-private-sentinel'
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await writeFile(sentinel, original)
    if (directory) {
      const link = join(home, 'linked'); await symlink(outside, link)
      await symlink(join(link, 'sentinel'), join(home, target))
    } else await symlink(sentinel, join(home, target))
    child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/session-resume.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_RESUME_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production' }), stdin: new Blob([JSON.stringify({ home, classifier: false, steps: reduce([user('u', 'p')]) })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try { const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code).not.toBe(0); expect(stdout).toBe(''); expect(stderr).toContain(go ? 'disposable HOME/config and ordinary private files' : 'ordinary private files'); expect(await readFile(sentinel, 'utf8')).toBe(original) } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
}, 30_000)
