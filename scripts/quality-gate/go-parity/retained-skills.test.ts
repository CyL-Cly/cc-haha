import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { retainedSkillsCases, type RetainedSkillsCase } from './retained-skills-fixtures'
import { assertTraceEqual } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/retained-skills')
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun required')
  buildHome = await mkdtemp(join(tmpdir(), 'retained-skills-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/attachments'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(rows: RetainedSkillsCase[], kind: 'source' | 'go' = 'source', label = rows[0]!.name) {
  const home = await mkdtemp(join(tmpdir(), 'retained-skills-home-'))
  try {
    await mkdir(join(home, '.claude'))
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/retained-skills.fixture.ts')] : [executable, '-test.run=^TestRetainedSkillsFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RETAINED_SKILLS_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }),
      stdin: new Blob([JSON.stringify({ home, rows })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifact, { recursive: true })
      await writeFile(join(artifact, label.replace(/\W+/g, '-') + '-' + kind + '.json'), JSON.stringify({ original: rows, code, stdout, stderr }, null, 2))
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('RETAINED_SKILLS_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities).toEqual({ blocked: [], secureReads: [], absentReads: [] })
        expect(result.sourceOnly.bun).toBe('1.3.14')
        expect(result.sourceOnly.revision).toBe('0d9b296af33f2b851fcbf4df3e9ec89751734ba4')
      }
      return result.observations
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
for (const row of retainedSkillsCases) test(`original retained payload self + independent Go: ${row.name}`, async () => {
  const source = await observe([row])
  assertTraceEqual([{ kind: 'state', value: source }], [{ kind: 'state', value: await observe([row]) }])
  assertTraceEqual([{ kind: 'state', value: source }], [{ kind: 'state', value: await observe([row], 'go') }])
}, 65000)
test('strict retained trace rejects independent behavioral mutations', async () => {
  const baseline = await observe(retainedSkillsCases, 'source', 'mutation-baseline')
  const mutations = [
    (v: any[]) => { v[0].output[0] = v[0].output[0].replace('### Skill: one', '### Skill: deduped') },
    (v: any[]) => { v[0].output[0] = v[0].output[0].replace('<system-reminder>', '') },
    (v: any[]) => { v.find(r => r.name === 'invoked-lone-utf16').output[0] = v.find(r => r.name === 'invoked-lone-utf16').output[0].replace('\\ud800', '\ufffd') },
    (v: any[]) => { v.find(r => r.name === 'invoked-shadowed-toString').error = null },
    (v: any[]) => { v.find(r => r.name === 'virtual-assistant-stops-before-filter').reordered.reverse() },
    (v: any[]) => { v.find(r => r.name === 'tool-result-string').output[0] += ' ' },
    (v: any[]) => { v.find(r => r.name === 'retained-no-tools').output = [] },
    (v: any[]) => { v.find(r => r.name === 'attachments-before-users').trace.pop() },
  ]
  for (const mutate of mutations) {
    const changed = structuredClone(baseline); mutate(changed)
    expect(() => assertTraceEqual([{ kind: 'state', value: baseline }], [{ kind: 'state', value: changed }])).toThrow()
  }
}, 30000)
for (const fault of ['home', 'profile', 'config', 'native', 'network'] as const) test('preimport isolation rejects ' + fault, async () => {
  const home = await mkdtemp(join(tmpdir(), 'retained-skills-guard-'))
  try {
    await mkdir(join(home, '.claude'))
    const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/retained-skills.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_RETAINED_SKILLS_FIXTURE: fault === 'profile' ? 'denied' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ...fault === 'config' ? { CLAUDE_CONFIG_DIR: home } : {} }),
      stdin: new Blob([JSON.stringify({ home: fault === 'home' ? home + '/wrong' : home, rows: [], ...['native', 'network'].includes(fault) ? { probe: fault } : {} })]), stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).toBe('')
    expect(stderr).toContain(['native', 'network'].includes(fault) ? 'blocked' : fault === 'profile' ? 'explicit private' : 'disposable HOME/config')
  } finally { await rm(home, { recursive: true, force: true }) }
})
