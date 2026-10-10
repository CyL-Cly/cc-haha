import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { requestMappingScenarios, type RequestMappingStep } from './request-mapping-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let executable: string
async function record(kind: 'typescript' | 'go', steps: RequestMappingStep[]): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'request-mapping-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/request-mapping.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }), stdin: new Blob([JSON.stringify({ homeDir: home, steps })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      return [{ kind: 'state', value: { decoded: JSON.parse(stdout), source: canonicalJsonSource(stdout) } }, { kind: 'stdio', value: { exitCode: code, stderr } }]
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

// This runs the real source with the release feature set. It does not compile
// a minified native sidecar or assert equality of runtime error stacks.
describe('request mapping actual TS release-feature self replay', () => {
  for (const [name, steps] of requestMappingScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', steps), await record('typescript', steps)) }, 40_000)
  }
})

describe('request mapping Go component parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'request-mapping-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'request-mapping.exe' : 'request-mapping')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/requestmapping/testdata/fixture'], {
      cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const [name, steps] of requestMappingScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', steps), await record('go', steps)) }, 40_000)
  }
})
