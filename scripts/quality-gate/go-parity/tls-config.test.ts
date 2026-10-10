import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { tlsConfigCases, type TLSConfigCase } from './tls-config-fixtures'
import { assertTraceEqual } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/tls-config')
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun required')
  buildHome = await mkdtemp(join(tmpdir(), 'tls-config-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/tlsconfig'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
async function observe(rows: TLSConfigCase[], kind: 'source' | 'go' = 'source', label = rows[0]!.name) {
  const home = await mkdtemp(join(tmpdir(), 'tls-config-home-'))
  try {
    await mkdir(join(home, '.claude'))
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/tls-config.fixture.ts')] : [executable, '-test.run=^TestTLSConfigFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TLS_CONFIG_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production' }),
      stdin: new Blob([JSON.stringify({ home, rows })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifact, { recursive: true })
      await writeFile(join(artifact, label.replace(/\W+/g, '-') + '-' + kind + '.json'), JSON.stringify({ original: rows, code, stdout, stderr }, null, 2))
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('TLS_CONFIG_RESULT:')[1].split('\n')[0])
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities).toEqual({ blocked: [], secureReads: [], absentReads: [] })
        expect(result.sourceOnly.bun).toBe('1.3.14')
        expect(result.sourceOnly.revision).toBe('0d9b296af33f2b851fcbf4df3e9ec89751734ba4')
      }
      return result.observations
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
for (const row of tlsConfigCases) test('original CA state self + independent Go: ' + row.name, async () => {
  const source = await observe([row])
  assertTraceEqual([{ kind: 'state', value: source }], [{ kind: 'state', value: await observe([row]) }])
  assertTraceEqual([{ kind: 'state', value: source }], [{ kind: 'state', value: await observe([row], 'go') }])
}, 65000)
test('strict CA comparison rejects cache, raw-data and identity mutations', async () => {
  const baseline = await observe(tlsConfigCases, 'source', 'mutation-baseline')
  const mutations = [
    (v: any[]) => { v[0].steps[2].certificates = { count: 146 } },
    (v: any[]) => { v.find(r => r.name === 'file-change-stale-until-clear').steps[1].certificates.sha256 = 'changed' },
    (v: any[]) => { v.find(r => r.name === 'extra-is-one-raw-item').steps[0].certificates.count++ },
    (v: any[]) => { v.find(r => r.name === 'array-push-reference-until-clear').steps[1].certificates.identity++ },
    (v: any[]) => { v.find(r => r.name === 'bom-and-newlines-preserved').steps[0].certificates.tail[0] = '"one\\ntwo\\n"' },
    (v: any[]) => { v.find(r => r.name === 'array-lone-utf16-preserved').steps[1].certificates.tail[1] = '"�"' },
    (v: any[]) => { v.find(r => r.name === 'missing-file-fallback-cached').steps[0].reads = [] },
    (v: any[]) => { v.find(r => r.name === 'node-options-5').steps[0].option = true },
  ]
  for (const mutate of mutations) {
    const changed = structuredClone(baseline); mutate(changed)
    expect(() => assertTraceEqual([{ kind: 'state', value: baseline }], [{ kind: 'state', value: changed }])).toThrow()
  }
}, 30000)
for (const fault of ['home', 'profile', 'config', 'native', 'network', 'filesystem'] as const) test('preimport TLS isolation rejects ' + fault, async () => {
  const home = await mkdtemp(join(tmpdir(), 'tls-config-guard-'))
  try {
    await mkdir(join(home, '.claude'))
    const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/tls-config.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TLS_CONFIG_FIXTURE: fault === 'profile' ? 'denied' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ...fault === 'config' ? { CLAUDE_CONFIG_DIR: home } : {} }),
      stdin: new Blob([JSON.stringify({ home: fault === 'home' ? home + '/wrong' : home, rows: [], ...['native', 'network', 'filesystem'].includes(fault) ? { probe: fault } : {} })]), stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).toBe('')
    expect(stderr).toContain(['native', 'network', 'filesystem'].includes(fault) ? 'blocked' : fault === 'profile' ? 'explicit private' : 'disposable HOME/config')
  } finally { await rm(home, { recursive: true, force: true }) }
})

for (const kind of ['source', 'go'] as const) test('original env/path isolation rejects outside input in ' + kind, async () => {
  const home = await mkdtemp(join(tmpdir(), 'tls-config-original-guard-'))
  try {
    await mkdir(join(home, '.claude'))
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/tls-config.fixture.ts')] : [executable, '-test.run=^TestTLSConfigFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TLS_CONFIG_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production' }),
      stdin: new Blob([JSON.stringify({ home, rows: [{ name: 'unsafe-original', operations: [{ op: 'env', value: { NODE_EXTRA_CA_CERTS: '/Library/Keychains/System.keychain' } }, { op: 'get' }] }] })]), stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).not.toContain('TLS_CONFIG_RESULT:')
    expect(stdout + stderr).toContain('private original env/path')
  } finally { await rm(home, { recursive: true, force: true }) }
})
