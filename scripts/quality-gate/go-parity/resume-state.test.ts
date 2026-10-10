import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, stat, symlink, utimes, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { stateCases, type StateCase } from './resume-state-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url)), artifact = join(root, 'artifacts/go-parity/resume-state'), allocationRoot = tmpdir()
let buildHome: string, executable: string
const contracts: unknown[] = []
const helperContracts: unknown[] = []
function assertContract(actual: unknown, expected: unknown) { expect(actual).toEqual(expected) }
beforeAll(async () => {
  buildHome = await mkdtemp(join(allocationRoot, 'resume-state-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/resumestate/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
  if (contracts.length) { await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, 'source-contract.json'), JSON.stringify({ source: 'actual Source loadConversationForResume; release TRANSCRIPT_CLASSIFIER', contracts, helperContracts }, null, 2) + '\n') }
})
async function initialize(row: StateCase) {
  for (const name of ['.claude', 'policy', 'private-plans', 'session.jsonl', 'resume-state-result.json']) await rm(join(row.input.home as string, name), { recursive: true, force: true })
  for (const directory of row.directories ?? []) await mkdir(directory, { recursive: true })
  const stats: Record<string, { modified: number, birth: number, size: number }> = {}
  for (const [path, value] of Object.entries(row.files)) {
    await mkdir(dirname(path), { recursive: true }); await writeFile(path, value)
    if (row.times?.[path] !== undefined) await utimes(path, row.times[path] / 1000, row.times[path] / 1000)
    const info = await stat(path)
    stats[path] = { modified: info.mtime.getTime(), birth: info.birthtime.getTime(), size: info.size }
  }
  return stats
}
async function run(row: StateCase, go: boolean) {
  const stats = await initialize(row), home = row.input.home as string
  const command = go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/resume-state.fixture.ts')]
  const child = Bun.spawn(command, { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RESUME_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', TZ: 'UTC' }), stdin: new Blob([JSON.stringify({ ...row.input, stat: stats })]), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 30_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, row.name + (go ? '-go' : '-source') + '-stderr.txt'), stderr)
    expect(code, stderr).toBe(0); expect(stderr).toBe(''); expect(JSON.parse(stdout)).toEqual({ result: 'resume-state-result.json' })
    const value = JSON.parse(await readFile(join(home, 'resume-state-result.json'), 'utf8'))
    if (row.input.liteReadFault) { const path = (row.input.liteReadFault as { path: string }).path; expect(value.sourceOnly.liteReadFaults).toEqual([{ operation: 'read', ordinal: 2, position: stats[path]!.size - 65536 }]) }
    if (!go) { expect(value.sourceOnly.capabilities.blocked).toEqual([]); expect(value.sourceOnly.capabilities.secureReads).toEqual([]); expect(value.sourceOnly.capabilities.absentReads).toEqual([]); if (row.input.uuidProbes) expect(value.sourceOnly.unprimedControlReads).toBe(1) }
    await writeFile(join(artifact, row.name + (go ? '-go' : '-source') + '-raw.json'), JSON.stringify(value, null, 2))
    return value.observation
  } finally { clearTimeout(timer); child.kill(); await child.exited }
}
for (const selected of stateCases('/fixture')) test('actual original-input resume orchestration: ' + selected.name, async () => {
  const home = await mkdtemp(join(allocationRoot, 'resume-state-case-'))
  try {
    const row = stateCases(home).find(row => row.name === selected.name)!
    const source = await run(row, false), replay = await run(row, false)
    assertContract(replay, source)
    assertContract(await run(row, true), source)
    contracts.push({ name: row.name, input: row.input, files: row.files, times: row.times, directories: row.directories, expected: source })
  } finally { await rm(home, { recursive: true, force: true }) }
}, 95_000)

for (const mode of ['tail-read', 'close', 'zero-head']) test('actual portable reader operation fault: ' + mode, async () => {
  const home = await mkdtemp(join(allocationRoot, 'resume-state-lite-helper-'))
  const path = join(home, 'session.jsonl'), original = 'original-head' + 'x'.repeat(150000) + 'original-tail'
  try {
    await mkdir(join(home, '.claude')); await writeFile(path, original)
    const info = await stat(path), row = stateCases(home).find(row => row.name === 'empty-original-log')!
    const input = { ...row.input, source: undefined, stat: { [path]: { modified: info.mtime.getTime(), birth: info.birthtime.getTime(), size: info.size } }, liteHelper: { path, mode } }
    async function invoke(go: boolean) {
      const child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/resume-state-lite.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RESUME_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', TZ: 'UTC' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const timer = setTimeout(() => child.kill('SIGKILL'), 30_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, 'lite-' + mode + (go ? '-go' : '-source') + '-stderr.txt'), stderr)
        expect(code, stderr).toBe(0); expect(stderr).toBe(''); expect(JSON.parse(stdout)).toEqual({ result: 'resume-state-result.json' })
        const result = JSON.parse(await readFile(join(home, 'resume-state-result.json'), 'utf8'))
        await writeFile(join(artifact, 'lite-' + mode + (go ? '-go' : '-source') + '-raw.json'), JSON.stringify({ input, result }, null, 2))
        expect(await readFile(path, 'utf8')).toBe(original)
        return result
      } finally { clearTimeout(timer); child.kill(); await child.exited }
    }
    const source = await invoke(false); assertContract(await invoke(false), source); assertContract(await invoke(true), source)
    expect(source.head).toBe(''); expect(source.tail).toBe('')
    expect(source.calls).toEqual([{ operation: 'read', ordinal: 1, position: 0 }, ...(mode === 'zero-head' ? [] : [{ operation: 'read', ordinal: 2, position: info.size - 65536 }]), { operation: 'close' }])
    helperContracts.push({ mode, bytes: original, expected: source })
  } finally { await rm(home, { recursive: true, force: true }) }
}, 95_000)

type Observation = { resultSource: string, phases: string[], effects: Record<string, string>, allocations: Array<Record<string, unknown>> }
for (const component of ['resultSource', 'phase order', 'copied bytes', 'allocation', 'metadata']) test('strict Source comparator catches real ' + component + ' mutant', () => {
  const name = component === 'copied bytes' ? 'file-history-copied' : component === 'allocation' ? 'plan-recover-user' : 'original-unknown-metadata'
  const row = contracts.find(row => (row as { name: string }).name === name) as { expected: Observation } | undefined
  expect(row, 'actual Source profile must run before comparator mutants').toBeDefined()
  const actual = structuredClone(row!.expected), expected = row!.expected
  if (component === 'resultSource') actual.resultSource = actual.resultSource.replace('"messages":', '"changedMessages":')
  if (component === 'phase order') [actual.phases[0], actual.phases[1]] = [actual.phases[1]!, actual.phases[0]!]
  if (component === 'copied bytes') { const key = Object.keys(actual.effects)[0]!; actual.effects[key] = '00' + actual.effects[key] }
  if (component === 'allocation') actual.allocations[0]!.value = 'changed-generation'
  if (component === 'metadata') { const result = JSON.parse(actual.resultSource); result.customTitle = 'changed-title'; actual.resultSource = JSON.stringify(result) }
  expect(() => assertContract(actual, expected)).toThrow()
})

for (const mode of ['unknown-field', 'prepared-history', 'jsonl-leaf-symlink', 'output-leaf-symlink', 'policy-directory-symlink']) test('both original-input guards reject before Source imports: ' + mode, async () => {
  const home = await mkdtemp(join(allocationRoot, 'resume-state-guard-')), outside = await mkdtemp(join(allocationRoot, 'resume-state-sentinel-'))
  try {
    await mkdir(join(home, '.claude')); await writeFile(join(home, '.claude', 'settings.json'), '{invalid-pre-import-tripwire')
    const sentinel = join(outside, 'protected'), original = 'outside sentinel bytes'
    await writeFile(sentinel, original)
    const input: Record<string, unknown> = { ...stateCases(home)[0]!.input, stat: {} }
    if (mode === 'unknown-field') input.unknownInput = true
    if (mode === 'prepared-history') input.history = [{ type: 'user', message: { role: 'user', content: 'Source processed history' } }]
    if (mode === 'jsonl-leaf-symlink') { input.jsonlPath = join(home, 'session.jsonl'); await symlink(sentinel, input.jsonlPath as string) }
    if (mode === 'output-leaf-symlink') await symlink(sentinel, join(home, 'resume-state-result.json'))
    if (mode === 'policy-directory-symlink') { await writeFile(join(outside, 'managed-settings.json'), original); await symlink(outside, join(home, 'policy'), 'dir') }
    for (const go of [false, true]) {
      const command = go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/resume-state.fixture.ts')]
      const child = Bun.spawn(command, { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RESUME_STATE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0); expect(stdout).toBe('')
        expect(stderr).toMatch(/original offline inputs|ordinary private files|unknown field/)
        expect(stderr).not.toContain('src/utils/')
        expect(await readFile(sentinel, 'utf8')).toBe(original)
        expect(await readFile(join(home, '.claude', 'settings.json'), 'utf8')).toBe('{invalid-pre-import-tripwire')
        if (mode === 'policy-directory-symlink') expect(await readFile(join(outside, 'managed-settings.json'), 'utf8')).toBe(original)
        await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, 'guard-' + mode + (go ? '-go' : '-source') + '.json'), JSON.stringify({ code, stdout, stderr, sentinelUnchanged: true }))
      } finally { clearTimeout(timer); child.kill(); await child.exited }
    }
  } finally { await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
}, 40_000)
