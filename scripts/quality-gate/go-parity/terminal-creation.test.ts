import { beforeAll, afterAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'
import { terminalCreationCases, terminalOriginalEnv } from './terminal-creation-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/terminal-creation')
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun required')
  buildHome = await mkdtemp(join(tmpdir(), 'terminal-creation-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-race', '-c', '-o', executable, './internal/apipresentation'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(kind: 'source' | 'go', label: string) {
  const home = await mkdtemp(join(tmpdir(), 'terminal-creation-home-'))
  try {
    await mkdir(join(home, '.claude'))
    const input = { home, env: terminalOriginalEnv, cases: terminalCreationCases }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/terminal-creation.fixture.ts')] : [executable, '-test.run=^TestTerminalCreationFixture$'], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TERMINAL_CREATION_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1' }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifact, { recursive: true })
      await writeFile(join(artifact, label + '-raw.json'), JSON.stringify({ input, code, stdout, stderr }, null, 2))
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('TERMINAL_CREATION_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual([])
        // The exported mapper's direct-env-key branch does not load managed
        // policy. Preserve that observation separately from full-query runs.
        expect(result.sourceOnly.capabilities.absentReads).toEqual([])
      }
      return result
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
test('real exported Source self replay + independent Go terminal creation', async () => {
  const first = await observe('source', 'source-first')
  const second = await observe('source', 'source-second')
  expect(second.rows).toEqual(first.rows)
  const candidate = await observe('go', 'go')
  for (const [index, item] of terminalCreationCases.entries()) {
    const row = candidate.rows[index]
    if (item.supported === false) { expect(row.capability).toBe(true); expect(row.allocations).toEqual([]); expect(row.uuidCalls).toBe(item.priorUUIDs ?? 0) }
    else expect(row).toEqual(first.rows[index])
  }
  // Expected is saved only AFTER both independent executions, and is never
  // placed in the candidate's constructor/presentation input.
  await writeFile(join(artifact, 'source-contract.json'), JSON.stringify({ env: terminalOriginalEnv, cases: terminalCreationCases, expected: first.rows }, null, 2))
}, 60000)

test('terminal comparator detects bytes, allocator and error flag mutations', async () => {
  const source = await observe('source', 'mutant-baseline')
  for (const mutate of [(row: any) => row.result += ' ', (row: any) => row.uuidCalls++, (row: any) => row.allocations.reverse(), (row: any) => row.result = row.result.replace('"isApiErrorMessage":true', '"isApiErrorMessage":false'), (row: any) => row.result = row.result.replace('Request timed out', 'Request timed out.')]) {
    const candidate = structuredClone(source.rows[0]); mutate(candidate); expect(() => assertTraceEqual([{ kind: 'state', value: source.rows[0] }], [{ kind: 'state', value: candidate }])).toThrow()
  }
}, 30000)

for (const kind of ['source', 'go'] as const) for (const fault of ['home', 'config', 'cwd', 'profile', 'environment', 'prepared', 'constructor', ...kind === 'source' ? ['native', 'network', 'filesystem'] : []]) test(`terminal creation ${kind} pre-business guard: ${fault}`, async () => {
  const home = await mkdtemp(join(tmpdir(), 'terminal-creation-guard-'))
  const outside = await mkdtemp(join(tmpdir(), 'terminal-creation-protected-'))
  const sentinel = join(outside, 'sentinel')
  try {
    await mkdir(join(home, '.claude')); await writeFile(sentinel, 'protected unchanged')
    const input = { home: fault === 'home' ? outside : home, env: { ...terminalOriginalEnv, ...fault === 'environment' ? { CLAUDE_CODE_USE_BEDROCK: '1' } : {} }, cases: fault === 'constructor' ? [{ name: 'original SDK own code unsupported', error: { kind: 'connection', code: 'Source-only-own-code' } }] : [terminalCreationCases[0]], ...fault === 'prepared' ? { preparedMessage: '{}' } : {}, ...['native', 'network', 'filesystem'].includes(fault) ? { probe: fault, probePath: sentinel } : {} }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/terminal-creation.fixture.ts')] : [executable, '-test.run=^TestTerminalCreationFixture$'], { cwd: fault === 'cwd' ? outside : home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TERMINAL_CREATION_FIXTURE: fault === 'profile' ? 'denied' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ...fault === 'config' ? { CLAUDE_CONFIG_DIR: outside } : {} }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, `guard-${kind}-${fault}.json`), JSON.stringify({ input, code, stdout, stderr }))
    expect(code).not.toBe(0); expect(stdout).not.toContain('TERMINAL_CREATION_RESULT:'); expect(stdout + stderr).toContain(fault === 'prepared' ? kind === 'source' ? 'private profile' : 'unknown field' : fault === 'constructor' ? 'unported original Error' : fault === 'environment' ? 'environment' : ['native', 'network', 'filesystem'].includes(fault) ? 'blocked' : fault === 'profile' && kind === 'source' ? 'private profile' : 'disposable HOME/config')
    expect(await readFile(sentinel, 'utf8')).toBe('protected unchanged')
  } finally { await rm(home, { recursive: true, force: true }); await rm(outside, { recursive: true, force: true }) }
}, 15000)
