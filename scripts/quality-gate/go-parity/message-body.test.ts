import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { messageBodyBoundaryObservations, messageBodyScenarios, type MessageBodyStep } from './message-body-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const allocationRoot = tmpdir()
let executable: string
async function record(kind: 'typescript' | 'go', steps: MessageBodyStep[], observeTransport = false): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'message-body-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable, '-test.run=^TestMessageBodyFixture$'] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/message-body.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_MESSAGE_BODY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }), stdin: new Blob([JSON.stringify({ homeDir: home, steps, observeTransport })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      // The Go package test executable appends its runner's PASS line. Only
      // this known runner metadata is outside the component's stdout record.
      const serialized = kind === 'go' ? stdout.split('\n').find(line => line.startsWith('MESSAGE_BODY_RESULT:'))?.slice('MESSAGE_BODY_RESULT:'.length) : stdout
      expect(serialized).toBeDefined()
      let decoded = JSON.parse(serialized!)
      if (kind === 'typescript') {
        expect(decoded.capabilities.blocked).toEqual([])
        if (steps.some(step => step.operation === 'prepare')) expect(decoded.capabilities.secureReads.length).toBeGreaterThan(0)
        expect(decoded.capabilities.secureReads.every((call: string) => call === 'read')).toBe(true)
        const { capabilities, ...observations } = decoded
        decoded = observations
      }
      expect(decoded.results).toHaveLength(steps.length)
      for (const [index, step] of steps.entries()) {
        if (step.operation !== 'prepare') continue
        const result = decoded.results[index]
        expect(result.after).toBe(result.before)
        if (step.expectedError) expect(result.error).toEqual(step.expectedError)
        else if (!step.observationOnly) {
          expect(result.error).toBeNull()
          expect(result.prepared).not.toBeNull()
          expect(result.signedBody).not.toBeNull()
        }
      }
      return [{ kind: 'state', value: { decoded, source: canonicalJsonSource(JSON.stringify(decoded)) } }, { kind: 'stdio', value: { exitCode: code, stderr } }]
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

// Real production signer + getAnthropicClient + pinned SDK. This is a resolved
// JSON request component; it does not prove normalized history or main loop.
describe('message body actual TS release-feature self replay', () => {
  for (const [name, steps] of messageBodyScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', steps, true), await record('typescript', steps, true)) }, 50_000)
  }
})

describe('message body pending SDK boundary observations', () => {
  for (const [name, steps] of messageBodyBoundaryObservations) {
    test(name, async () => { assertTraceEqual(await record('typescript', steps, true), await record('typescript', steps, true)) }, 50_000)
  }
})

describe('message body Go JSON component parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'message-body-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'message-body.exe' : 'message-body')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/provider'], {
      cwd: join(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const [name, steps] of messageBodyScenarios) {
    test(name, async () => {
      // Full credential validation, SDK encoding outside JSON, and malformed
      // Headers are explicit source-only observations, outside this component.
      const applicable = steps.filter(step => step.operation !== 'prepare' || !step.observationOnly)
      assertTraceEqual(await record('typescript', applicable), await record('go', applicable))
    }, 50_000)
  }
})
