import { beforeAll, afterAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { assertTraceEqual, type TraceEvent } from './trace'
import { httpMetadataCases } from './http-metadata-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const temporaryRoot = tmpdir()
const artifacts = join(root, 'artifacts/go-parity/http-metadata')
const trace = (value: any): TraceEvent[] => [{ kind: 'transport', value }]
let buildHome: string, executable: string, source: any

beforeAll(async () => {
  buildHome = await mkdtemp(join(temporaryRoot, 'http-metadata-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'http-metadata.exe' : 'http-metadata')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/provider'], {
    cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 170000)
  try {
    const [code, stderr, stdout] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr + stdout).toBe(0)
  } finally {
    clearTimeout(timer)
  }
}, 180000)

afterAll(async () => {
  if (buildHome) await rm(buildHome, { recursive: true, force: true })
})

async function observe(kind: 'source' | 'go', guard?: 'home' | 'config' | 'cwd') {
  const home = await mkdtemp(join(temporaryRoot, 'http-metadata-case-'))
  try {
    await mkdir(join(home, '.claude'))
    const sentinel = join(home, 'sentinel.json')
    await writeFile(sentinel, '{"protected":true}')
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_HTTP_METADATA_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: temporaryRoot,
      NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    })
    if (guard === 'config') env.CLAUDE_CONFIG_DIR = join(home, 'wrong')
    const fixture = join(root, 'scripts/quality-gate/go-parity/http-metadata.fixture.ts')
    const loader = join(home, 'tripwire.ts')
    if (guard && kind === 'source') {
      await writeFile(loader, `import { plugin } from 'bun'\nplugin({ name:'http-metadata-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents:'throw new Error("production reached")', loader:'ts' } }) } })\nawait import(${JSON.stringify(fixture)})`)
    }
    const child = Bun.spawn(kind === 'source' ? [process.execPath, '--no-env-file', guard ? loader : fixture] : [executable, '-test.run=^TestHTTPMetadataFixture$'], {
      cwd: guard === 'cwd' ? root : home, env,
      stdin: new Blob([JSON.stringify({ home: guard === 'home' ? join(home, 'missing') : home, cases: httpMetadataCases })]),
      stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 90000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      if (guard) {
        expect(code).not.toBe(0)
        expect(stdout).not.toContain('PRODUCTION_IMPORT_REACHED')
        expect(stdout + stderr).toContain('disposable HOME/config before')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":true}')
        return null
      }
      expect(code, stderr + stdout.slice(-2000)).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(kind === 'source' ? stdout : stdout.split('\n').find(line => line.startsWith('HTTP_METADATA_OBSERVATION '))!.slice('HTTP_METADATA_OBSERVATION '.length))
      if (kind === 'source') {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual([])
        const absentPaths = absentManagedSettingsFixturePaths()
        expect(result.sourceOnly.capabilities.absentReads.every((read: { path: string }) => absentPaths.includes(read.path))).toBe(true)
        expect(result.sourceOnly.sdkVersion).toBe('0.80.0')
        expect(result.sourceOnly.runtimeVersion).toBe('1.3.14')
        expect(result.sourceOnly.runtimeRevision).toBe('0d9b296af33f2b851fcbf4df3e9ec89751734ba4')
        await mkdir(artifacts, { recursive: true })
        await writeFile(join(artifacts, 'source-only-capabilities.json'), JSON.stringify(result.sourceOnly, null, 2))
      }
      return result.observation
    } finally {
      clearTimeout(timer)
    }
  } finally {
    await rm(home, { recursive: true, force: true })
  }
}

test('actual Source constructor/fetch/SDK/notice self replay from original header pairs', async () => {
  source = await observe('source')
  assertTraceEqual(trace(source), trace(await observe('source')))
  await mkdir(artifacts, { recursive: true })
  await writeFile(join(artifacts, 'source.json'), JSON.stringify({ cases: httpMetadataCases, observation: source }, null, 2))
}, 190000)

test('independent Go original header projection and raw HTTP1 SDK/notice match actual Source', async () => {
  source ??= await observe('source')
  assertTraceEqual(trace(source), trace(await observe('go')))
}, 95000)

for (const guard of ['home', 'config', 'cwd'] as const) {
  test(`HTTP metadata ${guard} guard precedes production imports and Go execution`, async () => {
    await observe('source', guard)
    await observe('go', guard)
  }, 95000)
}

test('metadata comparator detects JSON/iterator order, close, duplicate, cookie, notice and request drift', async () => {
  source ??= await observe('source')
  const mutations = [
    (value: any) => value.observations[0].headers.jsonSource = '{}',
    (value: any) => value.observations[0].headers.iterator.reverse(),
    (value: any) => {
      const headers = value.observations.find((item: any) => item.name === 'http-mixed-close').headers
      headers.jsonSource = headers.jsonSource.replace('keep-alive, CLOSE', 'keep-alive')
    },
    (value: any) => value.observations[0].headers.get[0][1] = 'duplicate-lost',
    (value: any) => value.observations.find((item: any) => item.name.startsWith('http-')).noticeSource = '{}',
    (value: any) => value.observations[0].headers.setCookies.reverse(),
    (value: any) => value.requestCount++,
  ]
  for (const mutate of mutations) {
    const mutant = structuredClone(source)
    mutate(mutant)
    expect(() => assertTraceEqual(trace(source), trace(mutant))).toThrow()
  }
}, 95000)
