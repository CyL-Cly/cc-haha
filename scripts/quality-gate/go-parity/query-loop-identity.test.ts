import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { identityDeprecatedCatalog, toolIdentityScenarios, type ToolIdentityScenario } from './query-loop-identity-fixtures'
const root = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string, executable: string
async function record(scenario: ToolIdentityScenario, go = false) {
  const home = await mkdtemp(join(tmpdir(), 'query-loop-identity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    child = Bun.spawn(go ? [executable, '-test.run=^TestToolIdentityFixture$'] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/query-loop-identity.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TOOL_IDENTITY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }),
      stdin: new Blob([JSON.stringify({ ...scenario, deprecatedCatalog: identityDeprecatedCatalog, home })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stdout + stderr).toBe(0)
      expect(stderr).toBe('')
      const output = JSON.parse(go ? stdout.split('\n').find(line => line.startsWith('TOOL_IDENTITY_RESULT:'))!.slice('TOOL_IDENTITY_RESULT:'.length) : stdout)
      if (!go) {
        expect(output.capabilities.blocked).toEqual([])
        expect(output.capabilities.secureReads).toEqual([])
        const [managed, dropIn] = absentManagedSettingsFixturePaths()
        expect(output.capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managed }, { operation: 'lstatSync', path: managed },
          { operation: 'openSync', path: managed }, { operation: 'readdirSync', path: dropIn },
          ...(output.observation.calls.length > 0 ? [{ operation: 'readdirSync', path: dropIn }] : []),
        ])
      }
      return output
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}
describe('actual Source findToolByName/runTools/runToolUse UTF16 identity', () => {
  for (const scenario of toolIdentityScenarios) test(scenario.name, async () => {
    const first = await record(scenario), second = await record(scenario)
    expect(first).toEqual(second)
    const uses = scenario.usesSources.map(raw => JSON.parse(raw))
    const ids = uses.filter((use, index) => first.observation.lookupNames[index] !== null && !scenario.aborted && !use.input?.__unparsedToolInput).map(use => JSON.stringify(use.id))
    expect(first.observation.calls).toEqual(ids)
    expect(first.observation.mapped).toEqual(ids)
    expect(first.observation.file).toBe(ids.length ? JSON.stringify(ids) : null)
    expect(first.sourceOnly.feedbackSources).toHaveLength(uses.length)
    for (const [index, raw] of first.sourceOnly.feedbackSources.entries()) {
      const block = JSON.parse(raw).message.content[0]
      expect(block.tool_use_id).toBe(uses[index].id)
      if (first.observation.lookupNames[index] === null || scenario.aborted || uses[index].input?.__unparsedToolInput) expect(block.is_error).toBe(true)
    }
    await mkdir(join(root, 'artifacts/go-parity/query-loop-identity-source'), { recursive: true })
    await writeFile(join(root, 'artifacts/go-parity/query-loop-identity-source', scenario.name + '.json'), JSON.stringify({ scenario, ...first }, null, 2))
  }, 35_000)
  for (const fault of ['HOME mismatch', 'missing profile', 'lone registry name', 'lone registry alias']) test(`Source rejects ${fault} before production imports`, async () => {
    const home = await mkdtemp(join(tmpdir(), 'query-loop-identity-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      await mkdir(join(home, '.claude'))
      const fixture = join(root, 'scripts/quality-gate/go-parity/query-loop-identity.fixture.ts')
      const sentinel = join(home, 'sentinel.json'), loader = join(home, 'tripwire.ts')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'identity-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(fixture)})`)
      child = Bun.spawn([process.execPath, '--no-env-file', loader], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TOOL_IDENTITY_FIXTURE: fault === 'missing profile' ? '' : '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production' }),
        stdin: new Blob([JSON.stringify({ ...toolIdentityScenarios[0], ...(fault === 'lone registry name' ? { registry: { name: '\ud800', aliases: [] } } : fault === 'lone registry alias' ? { registry: { name: 'FixtureWrite', aliases: ['\udfff'] } } : {}), home: fault === 'HOME mismatch' ? join(home, 'other') : home })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0)
        expect(stdout).toBe('')
        expect(stderr).toContain(fault === 'HOME mismatch' ? 'Tool identity fixture requires disposable HOME/config before production imports' : 'Tool identity requires explicit offline registry/model inputs')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
describe('Go raw UTF16 execution identity parity', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'query-loop-identity-build-'))
    executable = join(buildHome, 'identity')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/queryloop'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stdout + stderr).toBe(0)
  }, 180_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of toolIdentityScenarios) test(scenario.name, async () => {
    const source = await record(scenario), go = await record(scenario, true)
    expect(go.observation.lookupNames).toEqual(source.observation.lookupNames)
    expect(go.observation).toEqual(source.observation)
    expect(go.sourceOnly).toEqual(source.sourceOnly)
  }, 35_000)
  for (const fault of ['HOME mismatch', 'missing profile', 'lone registry name', 'lone registry alias']) test(`Go rejects ${fault} before tool execution`, async () => {
    const home = await mkdtemp(join(tmpdir(), 'query-loop-identity-go-guard-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      await mkdir(join(home, '.claude'))
      const sentinel = join(home, 'sentinel.json')
      await writeFile(sentinel, '{"protected":"unchanged"}')
      child = Bun.spawn([executable, '-test.run=^TestToolIdentityFixture$'], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_TOOL_IDENTITY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: fault === 'missing profile' ? '' : 'production' }),
        stdin: new Blob([JSON.stringify({ ...toolIdentityScenarios[0], ...(fault === 'lone registry name' ? { registry: { name: '\ud800', aliases: [] } } : fault === 'lone registry alias' ? { registry: { name: 'FixtureWrite', aliases: ['\udfff'] } } : {}), home: fault === 'HOME mismatch' ? join(home, 'other') : home })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(code).not.toBe(0)
        expect(stdout).not.toContain('TOOL_IDENTITY_RESULT:')
        expect(stdout + stderr).toContain(fault.startsWith('lone registry') ? 'unpaired UTF-16 model tool registry' : 'tool identity requires private HOME/config')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
        expect(await Bun.file(join(home, 'identity.txt')).exists()).toBe(false)
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
  }, 15_000)
})
