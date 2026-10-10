import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-tool-feedback-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'feedback.exe' : 'feedback')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/toolfeedback/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { operation: string; raw?: string; inputSource?: string; toolName?: string; toolID?: string; sourceUUID?: string }
function feedback(operation: 'invalid' | 'unknown', source: Pick<Step, 'raw' | 'inputSource'>, toolName = 'OfflineMissingTool') {
  return { operation, ...source, toolName, toolID: 'fixture-call', sourceUUID: 'source-assistant' }
}
const markerShapes = [
  'null', '[]', '{}', '42', '"raw"', '{"__unparsedToolInput":null}', '{"__unparsedToolInput":"bad"}',
  '{"__unparsedToolInput":{"raw":"bad","len":"3"}}', '{"__unparsedToolInput":{"raw":null,"len":3}}',
  '{"__unparsedToolInput":{"raw":"bad","len":null}}', '{"__unparsedToolInput":{"raw":"bad","len":3},"legitimate":true}',
  '{"__unparsedToolInput":{"raw":"","len":0}}', '{"__unparsedToolInput":{"raw":"bad","len":3,"future":true}}',
  '{"__unparsedToolInput":{"raw":"bad","len":1.5}}', '{"__unparsedToolInput":{"raw":"bad","len":1e400}}',
]
const fixtures: Array<[string, Step[]]> = [
  ['exact reserved marker recognition after history JSON parse', markerShapes.map(inputSource => ({ operation: 'is-marker', inputSource }))],
  ['marker bounds count UTF16 and retain lone surrogates', ['', '{"path":', 'x'.repeat(2047) + '😀tail', '\ud800\udfff<&>\n\t', 'x'.repeat(6000)].map(raw => ({ operation: 'create-marker', raw }))],
  ['actual executor rejects invalid arguments before schema permissions and side effects', ['', '{"path":"C:\\unsafe', 'x'.repeat(199) + '😀tail', '中文\n\t\ud800<&>', 'x'.repeat(3000)].map(raw => feedback('invalid', { raw }, 'InvalidTool\udfff<&>'))],
  ['malformed imported marker length follows JS interpolation exactly', ['0', '-1', '1.5', '9007199254740993', '1e400', '-1e400'].map(length => feedback('invalid', { inputSource: `{"__unparsedToolInput":{"raw":"history\\ud800","len":${length},"future":true}}` }))],
  ['unknown tool feedback preserves user text and source identity', ['OfflineMissingTool', 'mcp__offline_fixture__missing', 'Unknown\ud800<&>中文😀', ''].map(toolName => feedback('unknown', { inputSource: '{}' }, toolName))],
  ['ordered invalid and unknown envelopes share session UUID counter', [feedback('invalid', { raw: '{"broken":' }), feedback('unknown', { inputSource: '{}' }), feedback('invalid', { raw: 'second\ud800' })]],
]

async function record(kind: 'typescript' | 'go', steps: Step[]): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'go-tool-feedback-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/tool-feedback.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' }), stdin: new Blob([JSON.stringify({ homeDir: home, steps })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try { const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code, stderr).toBe(0); return [{ kind: 'state', value: { decoded: JSON.parse(stdout), source: canonicalJsonSource(stdout) } }, { kind: 'stdio', value: { exitCode: code, stderr } }] } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

for (const [name, steps] of fixtures) {
  test(`tool rejection actual TS self replay and Go envelope parity: ${name}`, async () => {
    const expected = await record('typescript', steps)
    assertTraceEqual(expected, await record('typescript', steps))
    assertTraceEqual(expected, await record('go', steps))
  }, 40_000)
}
