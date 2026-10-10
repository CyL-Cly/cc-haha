import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { requestContextWire } from './request-context-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir()
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(allocationRoot, 'session-resume-request-build-')); executable = join(buildHome, process.platform === 'win32' ? 'request.exe' : 'request')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessionresume/testdata/requestfixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
const sid = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
const user = (uuid: string, parentUuid: string | null, content: unknown, extra: Record<string, unknown> = {}) => ({ parentUuid, type: 'user', message: { role: 'user', content }, uuid, timestamp: '2025-01-01T00:00:00Z', sessionId: sid, ...extra })
const assistant = (uuid: string, parentUuid: string, content: unknown) => ({ parentUuid, type: 'assistant', message: { id: uuid, model: 'fixture-model', role: 'assistant', content, stop_reason: null, usage: { input_tokens: 0, output_tokens: 0 } }, uuid, timestamp: '2025-01-01T00:00:01Z', sessionId: sid })
const scenarios = [
  { name: 'release classifier preserves auto permission', entries: [user('u', null, 'auto original', { permissionMode: 'auto' }), assistant('a', 'u', [{ type: 'text', text: 'original answer' }])] },
  { name: 'release classifier removes bubble permission', entries: [user('u', null, 'bubble original', { permissionMode: 'bubble' }), assistant('a', 'u', [{ type: 'text', text: 'original answer' }])] },
  { name: 'completed original history', entries: [user('u', null, '01234567 original question'), assistant('a', 'u', [{ type: 'text', text: 'original answer' }])] },
  { name: 'interrupted original prompt with genuine sentinel', entries: [user('u', null, 'original interrupted')] },
  { name: 'unresolved original tool removed before next request', entries: [user('u', null, 'original tool'), assistant('a', 'u', [{ type: 'tool_use', id: 't', name: 'UnknownTool', input: { z: 1, a: '\ud800' } }])] },
  { name: 'paired tool result adds genuine continuation before first request', entries: [user('u', null, 'original tool'), assistant('a', 'u', [{ type: 'tool_use', id: 't', name: 'UnknownTool', input: { z: 1, a: '\ud800' } }]), user('r', 'a', [{ type: 'tool_result', tool_use_id: 't', content: 'persisted read' }])] },
  { name: 'ordered future and UTF16 history retained', entries: [user('u', null, '0123\ud80067 tail', { future: { z: 1, a: 3 }, permissionMode: 'unknown' }), assistant('a', 'u', [{ future: { z: 1, a: '\udc00' }, type: 'text', text: 'original answer' }])] },
  { name: 'compact-boundary next request uses recovered main chain', entries: [{ type: 'system', subtype: 'compact_boundary', uuid: 'c', timestamp: '2025-01-01T00:00:00Z' }, user('u', 'c', 'summary', { isCompactSummary: true }), assistant('a', 'u', [{ type: 'text', text: 'new answer' }])] },
]
async function record(entries: unknown[], go: boolean) {
  const home = await mkdtemp(join(allocationRoot, 'session-resume-request-'))
  const socket: Array<{ path: string, method: string, headers: Array<[string, string]>, body: string }> = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) { socket.push({ path: new URL(request.url).pathname + new URL(request.url).search, method: request.method, headers: [...request.headers], body: await request.text() }); return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'resume-response' } }) } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, '.claude', '.claude.json'), '{"userID":"fixture-resume-device"}')
    const original = entries.map(row => JSON.stringify(row)).join('\n') + '\n'
    await writeFile(join(home, 'session.jsonl'), original)
    child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/session-resume-request.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_RESUME_REQUEST: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin }), stdin: new Blob([JSON.stringify({ home, classifier: true, origin: server.url.origin, promptSource: JSON.stringify('next original prompt\ud800'), runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code, stderr).toBe(0); expect(stderr).toBe('')
      const result = JSON.parse(stdout)
      if (!go) expect(result.sourceOnly.capabilities.blocked).toEqual([])
      expect(socket).toHaveLength(1); expect(result.observation.requests).toHaveLength(1)
      const request = result.observation.requests[0]
      expect({ path: socket[0]!.path, method: socket[0]!.method, body: socket[0]!.body }).toEqual({ path: request.path, method: request.method, body: request.body })
      const headers = new Headers(socket[0]!.headers); for (const [name, value] of request.headers) expect(headers.get(name), name).toBe(value)
      expect(await readFile(join(home, 'session.jsonl'), 'utf8')).toBe(original)
      return result
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; server.stop(true); await rm(home, { recursive: true, force: true }) }
}
for (const scenario of scenarios) test('actual private JSONL → reducer → provider HTTP: ' + scenario.name, async () => {
  const source = await record(scenario.entries, false), replay = await record(scenario.entries, false), go = await record(scenario.entries, true)
  expect(replay.observation).toEqual(source.observation)
  expect(go.observation).toEqual(source.observation)
  expect(go.sourceOnly.prefetchAllocations).toEqual(source.sourceOnly.prefetchAllocations)
  const directory = join(root, 'artifacts/go-parity/session-resume/first-request'); await mkdir(directory, { recursive: true })
  await writeFile(join(directory, scenario.name + '.json'), JSON.stringify({ input: { entries: scenario.entries }, source, go, scope: 'Release TRANSCRIPT_CLASSIFIER enabled; internal features off. Real reader/reducer/requestcontext/provider. QueryEngine/print/hooks/restore not exercised; raw diagnostic/transport/stream lifecycle remains separately visible.' }, null, 2))
}, 100_000)
