import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { streamHistoryScenarios, type StreamHistoryObservation, type StreamHistoryScenario } from './stream-history-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'
import { absentManagedSettingsFixturePaths } from './client-options-capabilities'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const observationDir = join(rootDir, 'artifacts/go-parity/stream-history-source')
const allocationRoot = tmpdir()
type Capabilities = { blocked: string[]; secureReads: string[]; absentReads: Array<{ operation: string; path: string }> }
let executable: string
async function record(scenario: StreamHistoryScenario, kind: 'typescript' | 'go' = 'typescript'): Promise<{ observation: StreamHistoryObservation; trace: TraceEvent[]; capabilities?: Capabilities }> {
  const home = await mkdtemp(join(allocationRoot, 'stream-history-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable, '-test.run=^TestStreamHistoryFixture$'] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/stream-history.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, {
        CC_HAHA_STREAM_HISTORY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
        CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0',
        ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210',
      }), stdin: new Blob([JSON.stringify({ homeDir: home, wire: scenario.wire, model: scenario.model, nonInteractive: scenario.nonInteractive })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      let capabilities: Capabilities | undefined
      let serialized: string | undefined
      if (kind === 'go') serialized = stdout.split('\n').find(line => line.startsWith('STREAM_HISTORY_RESULT:'))?.slice('STREAM_HISTORY_RESULT:'.length)
      else {
        const decoded = JSON.parse(stdout) as { observationSource: string; capabilities: Capabilities }
        serialized = decoded.observationSource
        capabilities = decoded.capabilities
        expect(capabilities.blocked).toEqual([])
        expect(capabilities.secureReads.length).toBeGreaterThan(0)
        expect(capabilities.secureReads.every(call => call === 'read' || call === 'readAsync')).toBeTrue()
        const [managedFile, dropInDir] = absentManagedSettingsFixturePaths()
        expect(capabilities.absentReads).toEqual([
          { operation: 'lstatSync', path: managedFile },
          { operation: 'lstatSync', path: managedFile },
          { operation: 'openSync', path: managedFile },
          { operation: 'readdirSync', path: dropInDir },
        ])
      }
      expect(serialized).toBeDefined()
      const observation = JSON.parse(serialized!) as StreamHistoryObservation
      expect(observation.wire).toBe(scenario.wire)
      expect(observation.fetchCount).toBe(1)
      if (scenario.expectsStartEvent ?? scenario.wire.includes('message_start')) expect(observation.startMessageBytes).not.toBeNull()
      else expect(observation.startMessageBytes).toBeNull()
      expect(observation.normalizationError).toBeNull()
      for (const assistant of observation.assistants) {
        expect(JSON.parse(assistant.envelopeBytesAtYield).uuid).toBe(assistant.uuid)
        expect(JSON.parse(assistant.envelopeBytesAfterStream).uuid).toBe(assistant.uuid)
        expect(observation.timeline.some(row => row.kind === 'uuid' && row.value === assistant.uuid)).toBeTrue()
      }
      return { observation, capabilities, trace: [{ kind: 'state', value: { observation, source: canonicalJsonSource(serialized!) } }, { kind: 'stdio', value: { exitCode: code, stderr } }] }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

// Source-only first slice: actual production API generator and actual history
// normalization. This records complete envelopes and aliasing; it does not
// prove a Go agent loop, persistence, or the next complete provider request.
describe('stream history actual Source envelope self replay', () => {
  for (const scenario of streamHistoryScenarios) {
    test(scenario.name, async () => {
      const first = await record(scenario)
      const second = await record(scenario)
      assertTraceEqual(first.trace, second.trace)
      expect(first.capabilities).toEqual(second.capabilities)
      await mkdir(observationDir, { recursive: true })
      await writeFile(join(observationDir, `${scenario.name}.json`), `${JSON.stringify({ scenario: scenario.name, trace: first.trace, capabilities: first.capabilities }, null, 2)}\n`)
      if (scenario.name === 'reverse-block-stops-and-repeat') {
        const observation = first.observation
        expect(observation.assistants).toHaveLength(2)
        expect(observation.uuidCalls).toBe(2)
        expect(observation.assistants.map(message => message.usageSharesStartAfterStream)).toEqual([true, false])
        expect(observation.events.filter(row => JSON.parse(row.eventBytes).type === 'content_block_stop')).toHaveLength(2)
        const normalizedAssistant = observation.normalized.filter(row => JSON.parse(row.envelopeBytes).type === 'assistant')
        expect(normalizedAssistant).toHaveLength(1)
        expect(normalizedAssistant[0]!.sourceAssistantIndex).toBe(0)
        expect(normalizedAssistant[0]!.usageAliasesAssistantIndexes).toEqual([0])
        expect(JSON.parse(normalizedAssistant[0]!.messageBytes).content.map((block: { text: string }) => block.text)).toEqual(['second index', 'first index'])
      }
    }, 50_000)
  }
})

describe('stream history Go envelope parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'stream-history-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'stream-history.exe' : 'stream-history')
    const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/streamhistory'], {
      cwd: join(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const scenario of streamHistoryScenarios) {
    test(scenario.name, async () => { assertTraceEqual((await record(scenario)).trace, (await record(scenario, 'go')).trace) }, 50_000)
  }
})

describe('stream history Source boundary before production imports', () => {
  for (const fault of ['home mismatch', 'missing allocation root', 'dangling config symlink']) {
    test(fault, async () => {
      const home = await mkdtemp(join(allocationRoot, 'stream-history-guard-'))
      let child: ReturnType<typeof Bun.spawn> | undefined
      try {
        if (fault === 'dangling config symlink') await symlink(join(home, 'absent-target'), join(home, '.claude'))
        child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(rootDir, 'scripts/quality-gate/go-parity/stream-history.fixture.ts')], {
          cwd: home, env: createSandboxedTestEnvironment(home, {
            CC_HAHA_STREAM_HISTORY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: fault === 'missing allocation root' ? '' : allocationRoot,
            NODE_ENV: 'production', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1',
          }), stdin: new Blob([JSON.stringify({ homeDir: fault === 'home mismatch' ? join(home, 'different') : home, wire: '' })]), stdout: 'pipe', stderr: 'pipe',
        })
        const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
        try {
          const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          expect(code).not.toBe(0)
          expect(stdout).toBe('')
          expect(stderr).toContain('Stream history fixture requires disposable HOME/config before production imports')
        } finally { clearTimeout(timer) }
      } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
    }, 15_000)
  }
})
