import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, type TraceEvent } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const temporaryRoot = tmpdir()
let buildHome: string, executable: string
const environments: Array<string | undefined> = [undefined, '', '0', '-0', '-7', '5', '10.9', '+12ms', '0xFF', '1e3', '\ufeff\u00a0+2tail', '\u0085 3', '+', '-', 'Infinity', '9007199254740993', '9'.repeat(400)]
const delays = [
  ...[null, '', 'nonsense', '0', '-25', '10.9', '1e5', '\ufeff\u00a0+100seconds', '0x20', '9007199254740993', '9'.repeat(400)].map(header => ({ attempt: 10, header, maximum: 32000, random: 0.5 })),
  ...[0, 1, 6, 1000, -1000, 'NaN', 'Infinity', '-Infinity'].map(attempt => ({ attempt, header: null, maximum: 32000, random: 0.25 })),
  ...[0, '-0', -1, 'NaN', 'Infinity', '-Infinity'].map(maximum => ({ attempt: 1, header: null, maximum, random: 0 })),
  { attempt: 'NaN', header: null, maximum: '-Infinity', random: 0.5 },
]

async function record(environment: string | undefined, kind: 'typescript' | 'go') {
  const home = await mkdtemp(join(temporaryRoot, 'retry-delay-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_RETRY_DELAY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: temporaryRoot })
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/retry-delay.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, environment, delays })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const parsed = JSON.parse(stdout)
      if (kind === 'typescript') {
        expect(parsed.capabilities).toEqual({ blocked: [], secureReads: [] })
        delete parsed.capabilities
      }
      return parsed
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

describe('actual retry host delay/limit Source replay and independent Go values', () => {
  beforeAll(async () => {
    buildHome = await mkdtemp(join(temporaryRoot, 'retry-delay-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'retry-delay.exe' : 'retry-delay')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/retrypolicy/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const [index, environment] of environments.entries()) test(`environment ${JSON.stringify(environment)}`, async () => {
    const source = await record(environment, 'typescript')
    const trace = (value: unknown): TraceEvent[] => [{ kind: 'state', value: { profile: 'retry-host', value } }]
    assertTraceEqual(trace(source), trace(await record(environment, 'typescript')))
    assertTraceEqual(trace(source), trace(await record(environment, 'go')))
    const artifacts = join(root, 'artifacts/go-parity/retry-delay')
    await mkdir(artifacts, { recursive: true })
    await writeFile(join(artifacts, `environment-${index}.json`), JSON.stringify({ environment, delays, source }, null, 2))
  }, 45000)
})
