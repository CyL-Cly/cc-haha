import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { cliStartupEnvProfiles, type StartupEnvProfile } from './cli-startup-env-fixtures'

const root = resolve(import.meta.dir, '../../..')
const sourceOnly = process.env.CC_HAHA_CLI_STARTUP_ENV_SOURCE_ONLY === '1'
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Startup env requires pinned Bun1.3.14')
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(), 'cli-startup-env-build-'))
  executable = join(buildHome, 'startup-env.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/clistartupenv'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, out + err).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

export async function recordStartupEnv(source: boolean, profile: StartupEnvProfile) {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'cli-startup-env-parity-')))
  try {
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_FIXTURE_TEMP_ROOT: await realpath(tmpdir()), NODE_ENV: 'production', USER_TYPE: 'external',
      CLAUDE_CODE_ENABLE_XAA: '0', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1',
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', CC_HAHA_CLI_STARTUP_ENV_FIXTURE: '1',
    })
    const command = source ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/cli-startup-env.fixture.ts')] : [executable, '-test.run=^TestCLIStartupEnvFixture$']
    const child = Bun.spawn(command, { cwd: home, env, stdin: new Blob([JSON.stringify({ ...profile, home })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 20000)
    try {
      const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).arrayBuffer()])
      const stdout = Buffer.from(out).toString('utf8'), stderr = Buffer.from(err).toString('utf8')
      const dir = join(root, 'artifacts/go-parity/cli-startup-env-raw')
      await mkdir(dir, { recursive: true })
      await writeFile(join(dir, basename(home) + (source ? '-source' : '-go') + '.json'), JSON.stringify({ profile: profile.name, home, command, env, code, stdoutBase64: Buffer.from(out).toString('base64'), stderrBase64: Buffer.from(err).toString('base64') }, null, 2) + '\n')
      expect(code, stdout + stderr).toBe(0); expect(stderr).toBe('')
      const value = JSON.parse(stdout.replace(/\nPASS\n$/, '').replaceAll(home, '<HOME>'))
      if (source) { expect(value.capabilities.blocked).toEqual([]); expect(value.capabilities.secureReads).toEqual([]); expect(value.sourceOnly.eligibility).toBe(false) }
      return { name: value.name, results: value.results }
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
for (const profile of cliStartupEnvProfiles) {
  test('startup env actual Source self: ' + profile.name, async () => {
    const first = await recordStartupEnv(true, profile)
    expect(first.results.every((result: any) => result.error === null)).toBe(true)
    expect(first).toEqual(await recordStartupEnv(true, profile))
  }, 60000)
  ;(sourceOnly ? test.skip : test)('startup env independent Go: ' + profile.name, async () => { expect(await recordStartupEnv(false, profile)).toEqual(await recordStartupEnv(true, profile)) }, 60000)
}

test.each(['env raw type', 'UTF16 identity', 'source order', 'CA early return', 'cache raw number', 'own undefined'])('startup env comparator rejects %s', async mutation => {
  const profile = cliStartupEnvProfiles.find(p => p.name === (mutation === 'own undefined' ? 'unknown-settings-ordered-deep-merge' : mutation === 'cache raw number' ? 'catch-undefined-retains-prior-effort' : mutation === 'CA early return' ? 'extra-ca-truthy-boot-no-reads' : 'raw-global-scalars-utf16'))!
  const expected = await recordStartupEnv(true, profile), changed = structuredClone(expected)
  if (mutation === 'env raw type') changed.results[0].environment.find((f: any) => f.value.kind === 'number').value.kind = 'string'
  else if (mutation === 'UTF16 identity') changed.results[0].environment.find((f: any) => f.key.includes('\\ud800')).key = '"CC_HAHA_PROBE_�"'
  else if (mutation === 'source order') changed.results[0].effects.reverse()
  else if (mutation === 'CA early return') changed.results[0].effects.push({ kind: 'read', path: '<HOME>/.claude/settings.json' })
  else if (mutation === 'own undefined') changed.results[0].sessionView.fields = changed.results[0].sessionView.fields.filter((f: any) => f.key !== '"effortLevel"')
  else changed.results[0].sessionView.fields.find((f: any) => f.key === '"future"').value.items[0].negativeZero = true
  expect(() => expect(changed).toEqual(expected)).toThrow()
})
test.each(['home', 'config', 'extra-input'])('startup env fixture guard before imports %s', async fault => {
  const home = await mkdtemp(join(tmpdir(), 'cli-startup-env-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import {plugin} from 'bun'\nplugin({name:'tripwire',setup(build){build.onLoad({filter:/[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/},()=>{process.stdout.write('PRODUCTION_REACHED');return {contents:'throw new Error("production reached")',loader:'ts'}})}})\nawait import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/cli-startup-env.fixture.ts'))})`)
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_CLI_STARTUP_ENV_FIXTURE: '1', NODE_ENV: 'production', USER_TYPE: 'external', ...(fault === 'home' ? { HOME: home + '/wrong' } : {}), ...(fault === 'config' ? { CLAUDE_CONFIG_DIR: home + '/wrong' } : {}) })
    const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', loader], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, name: 'guard', operations: [], ...(fault === 'extra-input' ? { extra: true } : {}) })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0); expect(out).not.toContain('PRODUCTION_REACHED'); expect(err).toContain(fault === 'extra-input' ? 'declared private inputs' : 'disposable HOME/config before production imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})
if (process.env.CC_HAHA_CAPTURE_CLI_STARTUP_ENV === '1') {
  const rows = []
  for (const input of cliStartupEnvProfiles) rows.push({ input, expected: await recordStartupEnv(true, input) })
  await mkdir(join(root, 'backend/internal/clistartupenv/testdata'), { recursive: true })
  await writeFile(join(root, 'backend/internal/clistartupenv/testdata/contract.json'), JSON.stringify(rows, null, 2) + '\n')
}
