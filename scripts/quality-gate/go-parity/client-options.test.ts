import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { clientResolverCases, directClientSteps, type DirectClientStep } from './client-options-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
const fixtureEnvironment = { CC_HAHA_CLIENT_OPTIONS_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1' }
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'client-options-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'provider.test.exe' : 'provider.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/provider'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function record(kind: 'typescript' | 'go', steps: DirectClientStep[]): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'client-options-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'typescript' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/client-options.fixture.ts')] : [executable, '-test.run=^TestDirectClientOptionsFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment),
      stdin: new Blob([JSON.stringify({ homeDir: home, steps, resolvers: clientResolverCases, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const raw = kind === 'typescript' ? stdout : stdout.split('\n').find(line => line.startsWith('CLIENT_OPTIONS_RESULT:'))?.slice('CLIENT_OPTIONS_RESULT:'.length)
      expect(raw).toBeDefined()
      const decoded = JSON.parse(raw!)
      if (kind === 'typescript') {
        expect(decoded.profile).toBe('direct-bare')
        expect(decoded.capabilities.blocked).toEqual([])
        expect(decoded.capabilities.secureReads.length).toBeGreaterThan(0)
        expect(decoded.capabilities.secureReads.every((call: string) => call === 'read')).toBe(true)
      }
      const results = decoded.results
      expect(results).toHaveLength(steps.length)
      const artifacts = join(root, 'artifacts/go-parity/client-options')
      await mkdir(artifacts, { recursive: true })
      expect(decoded.resolverResults).toHaveLength(clientResolverCases.length)
      await writeFile(join(artifacts, `${kind}.json`), JSON.stringify(decoded, null, 2))
      return [...results, ...decoded.resolverResults].map((value: unknown) => ({ kind: 'transport', value: { observation: value, source: canonicalJsonSource(JSON.stringify(value)) } }))
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

for (const [name, directory, writing] of [
  ['leaf read', false, false], ['leaf write', false, true], ['directory read', true, false], ['directory write', true, true],
] as const) {
  test(`client options rejects symlink ${name} without touching the target`, async () => {
    const parent = await mkdtemp(join(allocationRoot, 'client-options-symlink-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const home = join(parent, 'home'), outside = join(parent, 'outside'), sentinel = join(outside, 'sentinel.txt')
      await mkdir(home)
      await mkdir(outside)
      await writeFile(sentinel, 'private-sentinel')
      const link = join(home, 'alias')
      await symlink(directory ? outside : sentinel, link, directory ? 'dir' : 'file')
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-options.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment),
        stdin: new Blob([JSON.stringify({ homeDir: home, steps: [], probeCapability: writing ? 'filesystem-write' : 'filesystem', probePath: directory ? join(link, 'sentinel.txt') : link })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).toBe(1)
        expect(stdout).toBe('')
        expect(stderr).toContain('Client options fixture blocked filesystem:' + (writing ? 'writeFileSync' : 'readFileSync'))
        expect(await readFile(sentinel, 'utf8')).toBe('private-sentinel')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(parent, { recursive: true, force: true }) }
  })
}

test('actual direct client defaults self replay and Go request profile', async () => {
  const expected = await record('typescript', directClientSteps)
  assertTraceEqual(expected, await record('typescript', directClientSteps))
  assertTraceEqual(expected, await record('go', directClientSteps))
}, 95_000)

for (const [name, step] of [
  ['initial HOME', { name: 'escape', env: { HOME: '/outside-fixture' } }],
  ['late HOME', { name: 'escape', env: {}, beforeFetchEnv: { HOME: '/outside-fixture' } }],
  ['late config', { name: 'escape', env: {}, beforeFetchEnv: { CLAUDE_CONFIG_DIR: '/outside-fixture' } }],
  ['cloud SDK selection', { name: 'escape', env: { CLAUDE_CODE_USE_BEDROCK: '1' } }],
  ['disable bare profile', { name: 'escape', env: { CLAUDE_CODE_SIMPLE: '0' } }],
  ['nonobject header profile', { name: 'escape', env: {}, headers: { kind: 'pairs', entries: [] } }],
] as const) {
  test(`client options rejects ${name} before importing production`, async () => {
    const home = await mkdtemp(join(tmpdir(), 'client-options-reject-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-options.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment),
        stdin: new Blob([JSON.stringify({ homeDir: home, steps: [step], runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).toBe(1)
        expect(stdout).toBe('')
        expect(stderr).toContain(name === 'nonobject header profile' ? 'Client options fixture supports only object request headers' : 'Client options fixture cannot override sandbox/provider-selection environment')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  })
}

for (const [name, overrides, symlinkHome] of [
  ['missing allocation root', { CC_HAHA_FIXTURE_TEMP_ROOT: '' }, false],
  ['wrong user profile', { USERPROFILE: '/outside-client-fixture' }, false],
  ['automatic env loading', { BUN_OPTIONS: '' }, false],
  ['non-bare auth', { CLAUDE_CODE_SIMPLE: '0' }, false],
  ['terminal shell environment', { CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '0' }, false],
  ['symlinked home', {}, true],
] as const) {
  test(`client options rejects ${name} at its disposable boundary`, async () => {
    const parent = await mkdtemp(join(allocationRoot, 'client-options-boundary-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const actualHome = join(parent, 'home')
      await mkdir(actualHome)
      const home = symlinkHome ? join(parent, 'alias') : actualHome
      if (symlinkHome) await symlink(actualHome, home, 'dir')
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-options.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, { ...fixtureEnvironment, ...overrides }),
        stdin: new Blob([JSON.stringify({ homeDir: home, steps: [], runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).toBe(1)
        expect(stdout).toBe('')
        expect(stderr).toContain(['non-bare auth', 'terminal shell environment'].includes(name) ? 'requires the isolated direct bare profile' : 'requires disposable HOME/config before production imports')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(parent, { recursive: true, force: true }) }
  })
}

for (const [probe, expected] of [
  ['subprocess', 'subprocess:execFileSync'], ['native', 'native:spawn'], ['network', 'network:fetch'], ['filesystem', 'filesystem:readFileSync'],
] as const) {
  test(`client options blocks ${probe} before production imports`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'client-options-capability-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-options.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, fixtureEnvironment),
        stdin: new Blob([JSON.stringify({ homeDir: home, steps: [], probeCapability: probe, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).toBe(1)
        expect(stdout).toBe('')
        expect(stderr).toContain('Client options fixture blocked ' + expected)
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  })
}
