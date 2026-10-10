import { beforeAll, afterAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { toolSchemaCases, type ToolSchemaCase } from './tool-schema-fixtures'
import { assertTraceEqual } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'

const root = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string, executable: string
async function record(scenario: ToolSchemaCase, kind: 'typescript' | 'go' = 'typescript') {
  const home = await mkdtemp(join(tmpdir(), 'tool-schema-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'typescript' ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/tool-schema.fixture.ts')] : [executable, '-test.run=^TestToolSchemaFixture$'], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TOOL_SCHEMA_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }), stdin: new Blob([JSON.stringify({ ...scenario, homeDir: home, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr + stdout).toBe(0)
      expect(stderr).toBe('')
      const raw = kind === 'typescript' ? stdout : stdout.split('\n').find(line => line.startsWith('TOOL_SCHEMA_RESULT:'))?.slice('TOOL_SCHEMA_RESULT:'.length)
      expect(raw).toBeDefined()
      const result = JSON.parse(raw!)
      if (kind === 'typescript') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads.every((call: string) => ['read', 'readAsync'].includes(call))).toBeTrue()
        const allowed = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads.every((call: { path: string }) => allowed.includes(call.path))).toBeTrue()
      }
      return { ...result, trace: [{ kind: 'transport' as const, value: result.observation }, { kind: 'stdio' as const, value: { exitCode: code, stderr } }] }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { force: true, recursive: true }) }
}
describe('Tool schema actual Source self replay', () => {
  for (const scenario of toolSchemaCases) test(scenario.name, async () => {
    const source = await record(scenario)
    assertTraceEqual(source.trace, (await record(scenario)).trace)
    const directory = join(root, 'artifacts/go-parity/tool-schema-source')
    await mkdir(directory, { recursive: true })
    await writeFile(join(directory, encodeURIComponent(scenario.name) + '.json'), JSON.stringify({ scenario, observation: source.observation, sourceOnly: source.sourceOnly }, null, 2))
  }, 70_000)
})
describe('Tool schema independent Go rendering and request parity', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'tool-schema-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'tool-schema.exe' : 'tool-schema')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/toolschema'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stdout + stderr).toBe(0)
  }, 180_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of toolSchemaCases) test(scenario.name, async () => assertTraceEqual((await record(scenario)).trace, (await record(scenario, 'go')).trace), 70_000)
})
describe('Tool schema comparison and isolation guards', () => {
  for (const field of ['description', 'input_schema', 'cache', 'order', 'request']) test(`mutation ${field}`, async () => {
    const scenario = field === 'request' ? toolSchemaCases.find(value => value.mode === 'request')! : toolSchemaCases.find(value => value.name === 'defer and cache overlay leave base unchanged')!
    const source = await record(scenario)
    const changed = structuredClone(source.observation)
    if (field === 'request') changed.fetches[0].body += ' '
    else if (field === 'order') changed.records[0].schemas.reverse()
    else {
      const schema = JSON.parse(changed.records[0].schemas[0])
      if (field === 'description') schema.description += ' '
      else if (field === 'input_schema') schema.input_schema.required.pop()
      else delete schema.cache_control
      changed.records[0].schemas[0] = JSON.stringify(schema)
    }
    expect(() => assertTraceEqual(source.trace, [{ kind: 'transport', value: changed }, source.trace[1]])).toThrow('First semantic difference')
  })
  for (const fault of ['home', 'root', 'symlink', 'env', 'network', 'native']) test(`guard ${fault}`, async () => {
    const home = await mkdtemp(join(tmpdir(), 'tool-schema-guard-'))
    try {
      const env = createSandboxedTestEnvironment(home, { CC_HAHA_TOOL_SCHEMA_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1' })
      const input: any = { ...toolSchemaCases[0], homeDir: home }
      if (fault === 'home') input.homeDir += '-wrong'
      else if (fault === 'root') env.CC_HAHA_FIXTURE_TEMP_ROOT = ''
      else if (fault === 'symlink') await symlink(join(home, 'absent'), join(home, '.claude'))
      else if (fault === 'env') input.env = { HOME: '/outside' }
      else input.probe = fault
      const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/tool-schema.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code).not.toBe(0)
      expect(stdout).toBe('')
      expect(stderr).toContain(['home', 'root', 'symlink'].includes(fault) ? 'requires disposable HOME/config before production imports' : fault === 'env' ? 'cannot override sandbox' : 'fixture blocked')
    } finally { await rm(home, { recursive: true, force: true }) }
  })
})
