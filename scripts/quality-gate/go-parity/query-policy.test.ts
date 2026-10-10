import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { queryPolicyScenarios, type QueryPolicyScenario } from './query-policy-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let executable: string

async function record(kind: 'typescript' | 'go', scenario: QueryPolicyScenario): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'query-policy-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/query-policy.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home), stdin: new Blob([JSON.stringify({ homeDir: home, startedAt: scenario.startedAt ?? 1_700_000_000_000, steps: scenario.steps })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      return [{ kind: 'state', value: { decoded: JSON.parse(stdout), source: canonicalJsonSource(stdout) } }, { kind: 'stdio', value: { exitCode: code, stderr } }]
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

describe('query token budget actual TS self replay', () => {
  for (const [name, scenario] of queryPolicyScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', scenario), await record('typescript', scenario)) }, 40_000)
  }
})

describe('query token budget Go component parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'query-policy-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'query-policy.exe' : 'query-policy')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/querypolicy/testdata/fixture'], {
      cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const [name, scenario] of queryPolicyScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', scenario), await record('go', scenario)) }, 40_000)
  }
})
