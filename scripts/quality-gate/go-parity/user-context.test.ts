import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm, mkdir, writeFile, realpath } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { userContextProfiles, type UserContextProfile } from './user-context-fixtures'
const root = resolve(import.meta.dir, '../../..'), sourceOnly = process.env.CC_HAHA_USER_CONTEXT_SOURCE_ONLY === '1'
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('User context requires pinned Bun1.3.14')
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(), 'user-context-build-'))
  executable = join(buildHome, 'context.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/usercontext'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, out + err).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
export async function recordContext(source: boolean, profile: UserContextProfile) {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'user-context-parity-')))
  try {
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', USER_TYPE: 'external', TZ: profile.timezone, CLAUDE_CODE_ENABLE_XAA: '0', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_CLAUDE_MDS: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', CC_HAHA_USER_CONTEXT_FIXTURE: '1' }, { PATH: '/usr/bin:/bin', LANG: 'en_US.UTF-8' })
    const command = source ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/user-context.fixture.ts')] : [executable, '-test.run=^TestUserContextFixture$']
    const child = Bun.spawn(command, { cwd: home, env, stdin: new Blob([JSON.stringify({ ...profile, home })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 20000)
    try {
      const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).arrayBuffer()])
      const stdout = Buffer.from(out).toString('utf8'), stderr = Buffer.from(err).toString('utf8'), artifact = join(root, 'artifacts/go-parity/user-context-raw')
      await mkdir(artifact, { recursive: true })
      await writeFile(join(artifact, basename(home) + (source ? '-source' : '-go') + '.json'), JSON.stringify({ name: profile.name, home, command, env, code, stdoutBase64: Buffer.from(out).toString('base64'), stderrBase64: Buffer.from(err).toString('base64'), originalInput: profile }, null, 2) + '\n')
      expect(code, stdout + stderr).toBe(0)
      expect(stderr).toBe('')
      const value = JSON.parse(stdout.replace(/\n?PASS\n$/, '').replaceAll(home, '<HOME>'))
      if (source) {
        expect(value.capabilities.blocked).toEqual([])
        expect(value.capabilities.secureReads).toEqual([])
      }
      return { name: value.name, results: value.results }
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
const goTest = sourceOnly ? test.skip : test
for (const profile of userContextProfiles) {
  test('user context actual Source self: ' + profile.name, async () => {
    const original = await recordContext(true, profile)
    expect(await recordContext(true, profile)).toEqual(original)
  }, 60000)
  goTest('user context independent Go: ' + profile.name, async () => { expect(await recordContext(false, profile)).toEqual(await recordContext(true, profile)) }, 60000)
}
if (process.env.CC_HAHA_CAPTURE_USER_CONTEXT === '1') {
  const rows = []
  for (const input of userContextProfiles) rows.push({ input, expected: await recordContext(true, input) })
  await mkdir(join(root, 'backend/internal/usercontext/testdata'), { recursive: true })
  await writeFile(join(root, 'backend/internal/usercontext/testdata/contract.json'), JSON.stringify(rows, null, 2) + '\n')
}

test.each(['memo-date', 'session-independent', 'future-identity', 'meta-uuid', 'memory-completion', 'utf16', 'schema-order'])('user context comparator rejects %s', async mutation => {
  const profile = userContextProfiles.find(p => p.name === (mutation === 'memory-completion' ? 'real-user-memory-completes-after-midnight' : mutation === 'utf16' ? 'override-lone-utf16-prefix' : mutation === 'schema-order' ? 'plugin-schema-ordered-strip-options-mcp' : 'midnight-utc-same-mode'))!
  const original = await recordContext(true, profile), changed = structuredClone(original)
  const reads = changed.results.filter((entry: any) => entry.step.kind === 'read')
  if (mutation === 'memo-date') reads[1].result.userContextJSON = reads[1].result.userContextJSON.replace('2026-10-01', '2026-10-02')
  else if (mutation === 'session-independent') reads[1].result.sessionDateJSON = '"2026-10-02"'
  else if (mutation === 'future-identity') reads[1].result.samePromise = false
  else if (mutation === 'meta-uuid') reads[0].result.prefixJSON = reads[0].result.prefixJSON.replace('000000000001', '000000000099')
  else if (mutation === 'memory-completion') reads[0].result.io[2].millis -= 2000
  else if (mutation === 'utf16') reads[0].result.userContextJSON = reads[0].result.userContextJSON.replace('\\ud800', '�')
  else changed.results[0].result.settingsJSON = changed.results[0].result.settingsJSON.replace('"model":"fixture-model",', '')
  expect(() => expect(changed).toEqual(original)).toThrow()
})
test.each(['home', 'config', 'extra-input'])('user context guards reject before Source imports %s', async fault => {
  const home = await mkdtemp(join(tmpdir(), 'user-context-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import {plugin} from 'bun'\nplugin({name:'tripwire',setup(build){build.onLoad({filter:/[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/},()=>{process.stdout.write('PRODUCTION_REACHED');return {contents:'throw new Error("production reached")',loader:'ts'}})}})\nawait import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/user-context.fixture.ts'))})`)
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_USER_CONTEXT_FIXTURE: '1', NODE_ENV: 'production', USER_TYPE: 'external', TZ: 'UTC', ...(fault === 'home' ? { HOME: home + '/wrong' } : {}), ...(fault === 'config' ? { CLAUDE_CONFIG_DIR: home + '/wrong' } : {}) })
    const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', loader], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, name: 'guard', timezone: 'UTC', steps: [], ...(fault === 'extra-input' ? { preparedHistory: [] } : {}) })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, out, err] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    await mkdir(join(root, 'artifacts/go-parity/user-context-guards'), { recursive: true })
    await writeFile(join(root, 'artifacts/go-parity/user-context-guards', fault + '.json'), JSON.stringify({ code, out, err, home }, null, 2))
    expect(code).not.toBe(0)
    expect(out).not.toContain('PRODUCTION_REACHED')
    expect(err).toContain(fault === 'extra-input' ? 'declared original private input' : 'disposable HOME/config before production imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})
