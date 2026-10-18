import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, realpath, rm, stat, utimes, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { controllerCases } from './session-controller-fixtures'
import { requestContextWire } from './request-context-fixtures'

const root = fileURLToPath(new URL('../../../', import.meta.url))
const artifact = join(root, 'artifacts/go-parity/session-controller/product-audit')
const wire = requestContextWire.replace('event: message_delta', 'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"SDK_NORMAL_RESPONSE"}}\n\nevent: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\nevent: message_delta')

async function actualSource(home: string, input: Record<string, unknown>, name: string, nativeExecutable?: string) {
  const socket: Array<{ body: string, headers: Array<[string, string]> }> = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    socket.push({ body: await request.text(), headers: [...request.headers] })
    return new Response(wire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'owned-sdk-audit' } })
  } })
  const env = createSandboxedTestEnvironment(home, {
    CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(),
    CLAUDE_CODE_ENTRYPOINT: 'sdk-cli', NODE_ENV: 'production', TZ: 'UTC',
    ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin,
    ANTHROPIC_CUSTOM_HEADERS: 'x-client-request-id: original-fixture-trace',
    CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1',
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1',
    CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0',
    USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin'),
  })
  const child = Bun.spawn(nativeExecutable ? [nativeExecutable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/session-controller.fixture.ts')], {
    cwd: home, env, stdin: new Blob([JSON.stringify({ ...input, origin: server.url.origin, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 35000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    const result = JSON.parse(await readFile(join(home, 'controller-result.json'), 'utf8'))
    await mkdir(artifact, { recursive: true })
    await writeFile(join(artifact, name + '-raw.json'), JSON.stringify({ input, code, stdout, stderr, socket, result }, null, 2))
    expect(code, stderr || stdout).toBe(0)
    expect(stderr).toBe('')
    if (!nativeExecutable) expect(result.sourceOnly.capabilities.blocked).toEqual([])
    expect(result.observation.requests).toEqual(socket.map(value => value.body))
    return { result, socket, frames: (nativeExecutable ? result.nativeOnly.stream.output : stdout.trim().split('\n')).map((line: string) => JSON.parse(line)) }
  }
  finally { clearTimeout(timer); child.kill(); await child.exited; server.stop(true) }
}

test('actual Source normal prior turn and selected persisted picker consumer retain raw scheduling outcomes', async () => {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'session-controller-product-audit-')))
  const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
  const outcomes: unknown[] = []
  try {
    for (const profile of ['cold', 'normal-prior-turn', 'normal-prior-interrupt']) {
      for (let index = 0; index < 5; index++) {
        for (const directory of ['.claude', 'policy', 'controller-result.json']) await rm(join(home, directory), { recursive: true, force: true })
        const stats: Record<string, unknown> = {}
        for (const [path, contents] of Object.entries(original.files)) {
          await mkdir(dirname(path), { recursive: true })
          await writeFile(path, contents)
          await utimes(path, 1700000000, 1700000000)
          const info = await stat(path)
          stats[path] = { modified: info.mtimeMs, birth: info.birthtimeMs, size: info.size }
        }
        const input = { ...original.input, stat: stats, nativeMain: true, nowMillis: 1767225600000, clockStepMillis: 1000, clockReadStepMillis: 1,
          ...(profile === 'normal-prior-turn' ? { sdkPrelude: { type: 'user', uuid: 'prelude-native-id', message: { role: 'user', content: 'PRELUDE_NATIVE_HUMAN' } } } : {}),
          ...(profile === 'normal-prior-interrupt' ? { sdkPrelude: [
            { type: 'user', uuid: 'prelude-later-id', priority: 'later', message: { role: 'user', content: 'PRELUDE_NATIVE_LATER' } },
            { type: 'user', uuid: 'prelude-now-id', priority: 'now', message: { role: 'user', content: 'PRELUDE_NATIVE_NOW' } },
          ] } : {}),
        }
        const accepted = await actualSource(home, input, `${profile}-${index}-accepted`)
        expect(accepted.socket).toHaveLength(profile === 'cold' ? 1 : 2)
        expect(accepted.frames.filter(frame => frame.type === 'result' && frame.subtype === 'success' && frame.result === 'SDK_NORMAL_RESPONSE')).toHaveLength(profile === 'cold' ? 1 : 2)
        // A new process reads this exact implementation's own physical file.
        // No canonical projection or Source-prepared transcript replaces it.
        const resumed = await actualSource(home, { ...original.input, stat: {}, nativeMain: true, pickerSelection: true, nowMillis: 1767225610000, clockStepMillis: 1000, clockReadStepMillis: 1,
          sdk: [{ type: 'user', uuid: 'audit-followup-id', message: { role: 'user', content: 'AUDIT_FOLLOWUP_HUMAN' } }],
        }, `${profile}-${index}-selected-resume`)
        expect(resumed.socket).toHaveLength(1)
        const selection = resumed.result.sourceOnly.pickerSelection
        expect(selection.selected.sessionId).toBe(original.input.sid)
        expect(selection.result.sessionId).toBe(original.input.sid)
        const selectedMessages = JSON.stringify(selection.result.messages)
        const body = JSON.parse(resumed.socket[0]!.body)
        for (const text of ['original user', 'old answer', 'later original', 'now original', 'next original', 'SDK_NORMAL_RESPONSE']) {
          expect(selectedMessages).toContain(text)
          expect(JSON.stringify(body.messages)).toContain(text)
        }
        expect(JSON.stringify(body.messages)).toContain('AUDIT_FOLLOWUP_HUMAN')
        expect(resumed.frames.some(frame => frame.type === 'result' && frame.result === 'SDK_NORMAL_RESPONSE')).toBe(true)
        // Titles remain product-visible observations. Do not normalize them,
        // assert a preferred branch, or modify the existing strict RED test.
        outcomes.push({ profile, index, picker: selection.selected, selected: selection.result, body,
          accepted: accepted.result.observation.transcripts, persisted: resumed.result.observation.transcripts,
          phases: accepted.result.sourceOnly.phases,
        })
      }
    }
    await writeFile(join(artifact, 'normal-prior-turn-selected-consumers.json'), JSON.stringify(outcomes, null, 2))
  }
  finally { await rm(home, { recursive: true, force: true }) }
}, 180000)

test('actual Go cold SDK file and selected picker retain Source title and next request with a monotonic clock', async () => {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'session-controller-monotonic-go-')))
  const buildHome = await mkdtemp(join(tmpdir(), 'session-controller-monotonic-build-'))
  const executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  try {
    const build = Bun.spawn(['go', 'build', '-p=2', '-o', executable, './internal/sessioncontroller/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
    const timeout = setTimeout(() => build.kill('SIGKILL'), 180000)
    try {
      const [code, stdout, stderr] = await Promise.all([build.exited, new Response(build.stdout).text(), new Response(build.stderr).text()])
      expect(code, stderr || stdout).toBe(0)
    } finally { clearTimeout(timeout); build.kill(); await build.exited }
    const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
    const results = []
    for (let index = 0; index < 5; index++) {
      const pair = []
      for (const implementation of ['source', 'go']) {
        for (const name of ['.claude', 'policy', 'controller-result.json']) await rm(join(home, name), { recursive: true, force: true })
        const stats: Record<string, unknown> = {}
        for (const [path, contents] of Object.entries(original.files)) {
          await mkdir(dirname(path), { recursive: true }); await writeFile(path, contents); await utimes(path, 1700000000, 1700000000)
          const info = await stat(path); stats[path] = { modified: info.mtimeMs, birth: info.birthtimeMs, size: info.size }
        }
        const selectedExecutable = implementation === 'go' ? executable : undefined
        const accepted = await actualSource(home, { ...original.input, stat: stats, nativeMain: true, nowMillis: 1767225600000, clockStepMillis: 1000, clockReadStepMillis: 1 }, `monotonic-${index}-${implementation}-accepted`, selectedExecutable)
        const resumed = await actualSource(home, { ...original.input, stat: {}, nativeMain: true, pickerSelection: true, nowMillis: 1767225610000, clockStepMillis: 1000, clockReadStepMillis: 1, sdk: [{ type: 'user', uuid: 'audit-followup-id', message: { role: 'user', content: 'AUDIT_FOLLOWUP_HUMAN' } }] }, `monotonic-${index}-${implementation}-selected-resume`, selectedExecutable)
        expect(accepted.socket).toHaveLength(1); expect(resumed.socket).toHaveLength(1)
        const picker = implementation === 'go' ? resumed.result.nativeOnly.stream.resumePicker : resumed.result.sourceOnly.resumePicker
        const selected = picker.find((row: { sessionId: string }) => row.sessionId === original.input.sid)
        expect(selected).toBeDefined()
        expect(resumed.frames.some((frame: { type: string, result?: string }) => frame.type === 'result' && frame.result === 'SDK_NORMAL_RESPONSE')).toBe(true)
        pair.push({ implementation, selected, accepted: accepted.result.observation.transcripts, body: JSON.parse(resumed.socket[0]!.body) })
      }
      expect(pair[1]!.body.messages).toEqual(pair[0]!.body.messages)
      results.push({ index, pair })
    }
    await writeFile(join(artifact, 'monotonic-source-go-selected-consumers.json'), JSON.stringify(results, null, 2))
    expect(results.map(row => row.pair[1]!.selected.firstPrompt)).toEqual(results.map(row => row.pair[0]!.selected.firstPrompt))
  } finally { await rm(home, { recursive: true, force: true }); await rm(buildHome, { recursive: true, force: true }) }
}, 240000)

test('diagnostic observer retains actual Source record batches and last-prompt setters', async () => {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'session-controller-record-observer-')))
  const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
  try {
    for (const profile of ['cold', 'warm-cleanup-diagnostic']) {
      for (const name of ['.claude', 'policy', 'controller-result.json']) await rm(join(home, name), { recursive: true, force: true })
      const stats: Record<string, unknown> = {}
      for (const [path, contents] of Object.entries(original.files)) {
        await mkdir(dirname(path), { recursive: true }); await writeFile(path, contents); await utimes(path, 1700000000, 1700000000)
        const info = await stat(path); stats[path] = { modified: info.mtimeMs, birth: info.birthtimeMs, size: info.size }
      }
      const accepted = await actualSource(home, { ...original.input, stat: stats, nativeMain: true, inspectRecords: true, ...(profile === 'warm-cleanup-diagnostic' ? { warmCleanup: true } : {}), nowMillis: 1767225600000, clockStepMillis: 1000, clockReadStepMillis: 1 }, `record-observer-${profile}`)
      expect(accepted.socket).toHaveLength(1)
      expect(accepted.result.sourceOnly.recordBatches.some((event: { phase: string }) => event.phase === 'insert:start')).toBe(true)
      expect(accepted.result.sourceOnly.recordBatches.some((event: { phase: string }) => event.phase === 'memo:set')).toBe(true)
    }
  } finally { await rm(home, { recursive: true, force: true }) }
}, 90000)
