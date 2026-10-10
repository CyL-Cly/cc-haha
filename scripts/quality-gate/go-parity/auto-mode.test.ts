import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const root = resolve(import.meta.dir, '../../..')
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-auto-mode-build-'))
  executable = join(buildHome, 'auto-mode-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/automode/testdata/fixture'], {
    cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe', stdin: 'ignore',
  })
  const timer = setTimeout(() => child.kill(), 170_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 180_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Profile = { name: string; operation?: 'defaults' | 'config'; sources: Record<string, unknown>; internal?: boolean; feature?: boolean; entry?: boolean; args?: string[] }
const profiles: Profile[] = [
  { name: 'defaults', operation: 'defaults', sources: {} },
  { name: 'feature-off', sources: { userSettings: { autoMode: { allow: ['ignored'] } } }, feature: false },
  { name: 'cold-missing', sources: {} },
  { name: 'empty-replaces', sources: { userSettings: { autoMode: { allow: [], soft_deny: [], hard_deny: [], environment: [] } } } },
  { name: 'defaults-in-place', sources: { userSettings: { autoMode: { allow: ['before', '$defaults', 'after', '$defaults'] } } } },
  { name: 'trust-order-and-excluded-project', sources: { userSettings: { autoMode: { allow: ['user'] } }, projectSettings: { autoMode: { allow: ['untrusted'] } }, localSettings: { autoMode: { allow: ['local'] } }, flagSettings: { autoMode: { allow: ['flag'] } }, policySettings: { autoMode: { allow: ['policy'] } } } },
  { name: 'invalid-source-rejected-whole', sources: { userSettings: { autoMode: { allow: ['must be rejected'], environment: [null] } }, localSettings: { autoMode: { environment: ['accepted'] } } } },
  { name: 'null-array-schema', sources: { userSettings: { autoMode: null }, localSettings: { autoMode: [] }, flagSettings: { autoMode: { allow: null } }, policySettings: { autoMode: { allow: [false] } } } },
  { name: 'classify-only-and-invalid-boolean', sources: { userSettings: { autoMode: { classifyAllShell: true } }, flagSettings: { autoMode: { allow: ['rejected'], classifyAllShell: 'true' } }, policySettings: { autoMode: { classifyAllShell: false } } } },
  { name: 'internal-deny-order', internal: true, sources: { userSettings: { autoMode: { soft_deny: ['soft'], deny: ['legacy'] } }, localSettings: { autoMode: { deny: ['local legacy'] } } } },
  { name: 'external-deny-ignored', sources: { userSettings: { autoMode: { deny: ['legacy'] } } } },
  { name: 'utf16-and-json-stringify', sources: { userSettings: { autoMode: { allow: ['\ud800', '\udc00', '💡', '<>&\u2028\u2029', 'line\nquote"', '\0', '$defaults ', ''], soft_deny: ['\ufeff'] } } } },
  { name: 'native-source-defaults', operation: 'defaults', entry: true, sources: {}, args: ['--bare', 'auto-mode', 'defaults'] },
  { name: 'native-source-config-file', entry: true, sources: { userSettings: { autoMode: { allow: ['first', '$defaults', 'last'], hard_deny: [], environment: ['private fixture'] } }, localSettings: { autoMode: { allow: ['local'] } } }, args: ['--bare', 'auto-mode', 'config'] },
]

async function run(source: boolean, profile: Profile) {
  const home = await mkdtemp(join(tmpdir(), 'auto-mode-parity-'))
  try {
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', ANTHROPIC_API_KEY: 'fixture-not-a-secret',
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1',
      CLAUDE_CODE_DISABLE_TERMINAL_TITLE: '1', CLAUDE_CODE_LOCAL_BUILD_TIME: '2000-01-01T00:00:00.000Z',
      CLAUDE_CODE_LOCAL_VERSION: '999.0.0-local', NO_COLOR: '1', TERM: 'dumb',
    })
    if (profile.entry) {
      await mkdir(join(home, '.claude'), { recursive: true })
      if (profile.sources.userSettings) await writeFile(join(home, '.claude/settings.json'), JSON.stringify(profile.sources.userSettings))
      if (profile.sources.localSettings) await writeFile(join(home, '.claude/settings.local.json'), JSON.stringify(profile.sources.localSettings))
    }
    const operation = profile.operation ?? 'config'
    const child = Bun.spawn(source ? [process.execPath, '--no-env-file', ...(profile.feature === false ? [] : ['--feature=TRANSCRIPT_CLASSIFIER']), join(root, 'scripts/quality-gate/go-parity/auto-mode.fixture.ts')] : [executable], {
      cwd: home, env, stdin: new Blob([JSON.stringify({ home, operation, args: profile.args ?? [], entry: profile.entry ?? false, sources: profile.sources, internal: profile.internal,
        snapshot: { TranscriptClassifier: profile.feature !== false, AnthropicInternal: profile.internal ?? false, Sources: profile.sources } })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 20_000)
    try {
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      let evidence: any
      if (source) evidence = JSON.parse(await readFile(join(home, 'auto-mode-evidence.json'), 'utf8'))
      return { exitCode, stdout, stderr, evidence }
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}

test('Go embeds the original Source template, rather than rendered expected rules', async () => {
  expect(await readFile(join(root, 'backend/internal/automode/permissions_external.txt'))).toEqual(await readFile(join(root, 'src/utils/permissions/yolo-classifier-prompts/permissions_external.txt')))
})
for (const profile of profiles) {
  test((profile.entry ? 'actual Source executable output versus Go handler component (Go startup pending): ' : 'actual trusted-config getter/handler parity: ') + profile.name, async () => {
    const reference = await run(true, profile), repeat = await run(true, profile), candidate = await run(false, profile)
    expect(reference.exitCode, reference.stderr).toBe(0)
    expect(candidate.exitCode, candidate.stderr).toBe(0)
    expect(reference.evidence.blocked).toEqual([])
    expect(reference.evidence.secureReads).toEqual([])
    expect({ ...reference, evidence: undefined }).toEqual({ ...repeat, evidence: undefined })
    expect({ ...candidate, evidence: undefined }).toEqual({ ...reference, evidence: undefined })
    expect(reference.stderr).toBe('')
    expect(reference.evidence.network.filter((request: any) => request.method !== 'HEAD')).toEqual([])
    if (profile.entry) {
      expect(reference.evidence.checkpoints).toContain('preAction_after_init')
      expect(reference.evidence.checkpoints).toContain('init_function_end')
      expect(reference.evidence.checkpoints).toContain('preAction_after_sinks')
      expect(reference.evidence.checkpoints).toContain('preAction_after_migrations')
      expect(reference.evidence.network).toEqual([{ method: 'HEAD', path: '/' }])
    }
  }, 60_000)
}

test.each(['home', 'config', 'cwd'])('auto-mode fixture rejects invalid %s before any production import', async boundary => {
  const home = await mkdtemp(join(tmpdir(), 'auto-mode-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'auto-mode-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/auto-mode.fixture.ts'))})`)
    const child = Bun.spawn([process.execPath, '--no-env-file', loader], {
      cwd: boundary === 'cwd' ? root : home,
      env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), ...(boundary === 'home' ? { HOME: join(home, 'wrong') } : {}), ...(boundary === 'config' ? { CLAUDE_CONFIG_DIR: join(home, 'wrong') } : {}) }),
      stdin: new Blob([JSON.stringify({ home, sources: {} })]), stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).not.toContain('PRODUCTION_IMPORT_REACHED')
    expect(stderr).toContain('fixture requires disposable HOME/config before production imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})
