import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { requestContextCases, type RequestContextCase } from './request-context-fixtures'
import { assertTraceEqual, type TraceEvent } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let buildHome: string, executable: string
export async function recordRequestContext(scenario: RequestContextCase, kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(allocationRoot, 'request-context-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'typescript' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/request-context.fixture.ts')] : [executable, '-test.run=^TestRequestContextFixture$'], {
      cwd: home, env: createSandboxedTestEnvironment(home, {
        CC_HAHA_REQUEST_CONTEXT_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1',
        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', ANTHROPIC_API_KEY: 'offline-fixture-key',
      }), stdin: new Blob([JSON.stringify({ ...scenario, homeDir: home, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const raw = kind === 'typescript' ? stdout : stdout.split('\n').find(line => line.startsWith('REQUEST_CONTEXT_RESULT:'))?.slice('REQUEST_CONTEXT_RESULT:'.length)
      expect(raw).toBeDefined()
      const result = JSON.parse(raw!)
      expect(result.observation.fetches).toHaveLength(1)
      if (scenario.name === 'model default deepseek-flash original id and proxy path') {
        const fetch = result.observation.fetches[0]
        const body = JSON.parse(fetch.body)
        expect(body.model).toBe('deepseek-flash')
        expect(body.max_tokens).toBe(32_000)
        expect(fetch.path).toBe('/proxy/providers/fixture/v1/messages?beta=true')
      }
      if (kind === 'typescript') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads.length).toBeGreaterThan(0)
        expect(result.sourceOnly.capabilities.secureReads.every((call: string) => call === 'read' || call === 'readAsync')).toBeTrue()
        expect(result.sourceOnly.wireRequests).toHaveLength(1)
        const [managedFile, dropInDir] = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managedFile }, { operation: 'lstatSync', path: managedFile },
          { operation: 'openSync', path: managedFile }, { operation: 'readdirSync', path: dropInDir },
        ])
        // The actual Bun fetch adds its own transport headers. Preserve them
        // separately; the migrated comparison boundary is complete pre-fetch
        // SDK/application headers, not a claim of raw HTTP wire equivalence.
        const wire = result.sourceOnly.wireRequests[0]
        expect(wire.body).toBe(result.observation.fetches[0].body)
        expect(wire.path).toBe(result.observation.fetches[0].path)
      }
      const wire = (kind === 'typescript' ? result.sourceOnly : result.goOnly).wireRequests[0]
      expect(wire.body).toBe(result.observation.fetches[0].body)
      expect(wire.path).toBe(result.observation.fetches[0].path)
      for (const pair of result.observation.fetches[0].headers) expect(wire.headers).toContainEqual(pair)
      if (kind === 'go') {
        const directory = join(root, 'artifacts/go-parity/request-context-go')
        await mkdir(directory, { recursive: true })
        await writeFile(join(directory, encodeURIComponent(scenario.name) + '.json'), JSON.stringify(result, null, 2))
      }
      const trace: TraceEvent[] = [{ kind: 'transport', value: result.observation }, { kind: 'stdio', value: { exitCode: code, stderr } }]
      return { ...result, trace }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

describe('Request context actual Source self replay', () => {
  for (const scenario of requestContextCases) test(scenario.name, async () => {
    const source = await recordRequestContext(scenario)
    assertTraceEqual(source.trace, (await recordRequestContext(scenario)).trace)
    const directory = join(root, 'artifacts/go-parity/request-context-source')
    await mkdir(directory, { recursive: true })
    await writeFile(join(directory, encodeURIComponent(scenario.name) + '.json'), JSON.stringify({ scenario, observation: source.observation, sourceOnly: source.sourceOnly }, null, 2))
  }, 50_000)
})

describe('Request context independently constructed Go request parity', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(allocationRoot, 'request-context-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'request-context.exe' : 'request-context')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/requestcontext'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of requestContextCases) test(scenario.name, async () => assertTraceEqual((await recordRequestContext(scenario)).trace, (await recordRequestContext(scenario, 'go')).trace), 50_000)
})

describe('Request context comparator and pre-import guards', () => {
  for (const field of ['body', 'headers', 'path', 'history', 'uuid']) test(`mutation ${field}`, async () => {
    const source = await recordRequestContext(requestContextCases[0]!)
    const changed = structuredClone(source.observation)
    if (field === 'body') changed.fetches[0].body += ' '
    else if (field === 'headers') changed.fetches[0].headers.pop()
    else if (field === 'path') changed.fetches[0].path += '&x=1'
    else if (field === 'history') changed.normalizedHistory[0].messageSource += ' '
    else changed.uuidCalls++
    expect(() => assertTraceEqual(source.trace, [{ kind: 'transport', value: changed }, source.trace[1]])).toThrow('First semantic difference')
  })
  for (const fault of ['home', 'root', 'symlink', 'env', 'network', 'native']) test(`guard ${fault}`, async () => {
    const home = await mkdtemp(join(allocationRoot, 'request-context-guard-'))
    try {
      const env = createSandboxedTestEnvironment(home, { CC_HAHA_REQUEST_CONTEXT_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1' })
      const input: any = { ...requestContextCases[0], homeDir: home }
      if (fault === 'home') input.homeDir += '-wrong'
      else if (fault === 'root') env.CC_HAHA_FIXTURE_TEMP_ROOT = ''
      else if (fault === 'symlink') await symlink(join(home, 'absent'), join(home, '.claude'))
      else if (fault === 'env') input.env = { HOME: '/outside' }
      else input.probe = fault
      const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/request-context.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).toBe('')
      expect(stderr).toContain(['home', 'root', 'symlink'].includes(fault) ? 'requires disposable HOME/config before production imports' : fault === 'env' ? 'cannot override sandbox' : 'fixture blocked')
    } finally { await rm(home, { recursive: true, force: true }) }
  })
})
