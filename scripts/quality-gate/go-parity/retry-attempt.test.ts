import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, type TraceEvent } from './trace'
import { retryAttemptCases, type AttemptCase } from './retry-attempt-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url)), temporaryRoot = tmpdir()
let buildHome: string, executable: string
const traces = (value: unknown): TraceEvent[] => [{ kind: 'state', value: { profile: 'retry-attempt', value } }]
beforeAll(async () => {
  buildHome = await mkdtemp(join(temporaryRoot, 'retry-attempt-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'retry-attempt.exe' : 'retry-attempt')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/retrypolicy/testdata/attempt-fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function observe(input: AttemptCase, kind: 'typescript' | 'go', guard?: 'home' | 'config' | 'cwd') {
  const home = await mkdtemp(join(temporaryRoot, 'retry-attempt-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_RETRY_ATTEMPT_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: temporaryRoot, CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'fixture-not-a-real-key', ANTHROPIC_BASE_URL: 'http://127.0.0.1:1' })
    await mkdir(join(home, '.claude'), { recursive: true })
    await writeFile(join(home, '.claude/settings.json'), '{"fastMode":true,"fixtureUnknown":"preserved"}')
    const sentinel = join(home, 'sentinel.json')
    await writeFile(sentinel, '{"protected":"unchanged"}')
    if (guard === 'config') env.CLAUDE_CONFIG_DIR = join(home, 'wrong-config')
    const fixture = join(root, 'scripts/quality-gate/go-parity/retry-attempt.fixture.ts')
    const loader = join(home, 'tripwire.ts')
    if (guard) await writeFile(loader, `import { plugin } from 'bun'\nplugin({ name: 'retry-attempt-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })\nawait import(${JSON.stringify(fixture)})`)
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', guard ? loader : fixture], { cwd: guard === 'cwd' ? root : home, env,
      stdin: new Blob([JSON.stringify({ ...input, home: guard === 'home' ? join(home, 'missing') : home })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      if (guard) { expect(code).not.toBe(0); expect(stdout).toBe(''); expect(stderr).toContain('disposable HOME/config before'); expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}'); return null }
      expect(code, stderr).toBe(0); expect(stderr).toBe('')
      const parsed = JSON.parse(stdout)
      if (kind === 'typescript') { expect(parsed.sourceOnly.capabilities.blocked).toEqual([]); delete parsed.sourceOnly }
      return parsed.observation
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}
const sourceGoldens: Array<{ input: AttemptCase; observation: unknown }> = []
for (const [index, input] of retryAttemptCases.entries()) {
  test(`Source retry generator self-replay: ${input.name}`, async () => {
    const source = await observe(input, 'typescript')
    assertTraceEqual(traces(source), traces(await observe(input, 'typescript')))
    sourceGoldens[index] = { input, observation: source }
    const artifacts = join(root, 'artifacts/go-parity/retry-attempt')
    await mkdir(artifacts, { recursive: true })
    await writeFile(join(artifacts, `source-${index}.json`), JSON.stringify({ input, observation: source }, null, 2))
  }, 45000)
  test(`Go complete request-attempt scheduler vs Source: ${input.name}`, async () => {
    const source = sourceGoldens[index]?.observation ?? await observe(input, 'typescript')
    assertTraceEqual(traces(source), traces(await observe(input, 'go')))
  }, 45000)
}
for (const boundary of ['home', 'config', 'cwd'] as const) test(`attempt rejects ${boundary} before imports or execution`, async () => { await observe(retryAttemptCases[0], 'typescript', boundary); await observe(retryAttemptCases[0], 'go', boundary) }, 45000)
test('attempt comparator detects retry budget, client, order, notice and terminal drift', async () => {
  const source = await observe(retryAttemptCases.find(input => input.name.startsWith('401 invalidates'))!, 'typescript')
  for (const change of [(value: any) => value.trace.reverse(), (value: any) => value.result.kind = 'raw-error', (value: any) => value.uuidCalls++, (value: any) => value.trace.find((item: any) => item.kind === 'yield').notice.retryInMs++, (value: any) => value.trace.find((item: any) => item.kind === 'operation').context.model = 'changed', (value: any) => value.trace.find((item: any) => item.kind === 'yield').wire = '{}']) {
    const mutant = structuredClone(source); change(mutant); expect(() => assertTraceEqual(traces(source), traces(mutant))).toThrow()
  }
}, 45000)
test('multiple original Headers JSON names remain a strict pending mismatch', async () => {
  const input: AttemptCase = { name: 'original multi-header notice pending', failures: [{ status: 500, message: 'upstream', headers: { 'request-id': 'original-id', 'x-original': 'value' } }] }
  const source = await observe(input, 'typescript')
  assertTraceEqual(traces(source), traces(await observe(input, 'typescript')))
  const go = await observe(input, 'go')
  expect(go.result.kind).toBe('raw-error')
  expect(go.result.error.message).toContain('original Bun Headers JSON enumeration')
  expect(() => assertTraceEqual(traces(source), traces(go))).toThrow()
  const artifacts = join(root, 'artifacts/go-parity/retry-attempt')
  await mkdir(artifacts, { recursive: true })
  await writeFile(join(artifacts, 'multi-header-notice-pending.json'), JSON.stringify({ status: 'pending-mismatch', input, source, go, limitation: 'A passing negative check is not Source/Go equivalence.' }, null, 2))
}, 45000)
