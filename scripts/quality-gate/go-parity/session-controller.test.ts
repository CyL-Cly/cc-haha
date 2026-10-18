import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, realpath, rm, stat, symlink, utimes, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { controllerCases, type ControllerCase } from './session-controller-fixtures'
import { requestContextWire } from './request-context-fixtures'
const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir(), artifact = join(root, 'artifacts/go-parity/session-controller')
let buildHome: string, executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(allocationRoot, 'session-controller-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/sessioncontroller/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  }
  finally {
    clearTimeout(timer)
  }
}, 190000)
afterAll(async () => {
  if (buildHome)
    await rm(buildHome, { recursive: true, force: true })
})
async function initialize(row: ControllerCase) {
  const home = row.input.home as string
  for (const name of ['.claude', 'policy', 'controller-result.json'])
    await rm(join(home, name), { recursive: true, force: true })
  const stats: Record<string, unknown> = {}
  for (const [path, value] of Object.entries(row.files)) {
    await mkdir(dirname(path), { recursive: true })
    await writeFile(path, value)
    await utimes(path, 1700000000, 1700000000)
    const info = await stat(path)
    stats[path] = { modified: info.mtimeMs, birth: info.birthtimeMs, size: info.size }
  }
  return stats
}
async function run(row: ControllerCase, go: boolean) {
  const home = row.input.home as string, stats = await initialize(row), socket: Array<{
    path: string
    body: string
    headers: Array<[
      string,
      string
    ]>
  }> = []
  await mkdir(artifact, { recursive: true })
  // Repeated profiles still expose the latest result at the original path,
  // while every physical attempt keeps its own immutable failure evidence.
  const attempt = await mkdtemp(join(artifact, row.name + (go ? '-go-' : '-source-')))
  const saveEvidence = async (name: string, content: string | Uint8Array) => {
    await writeFile(join(attempt, name), content, { flag: 'wx' })
    await writeFile(join(artifact, name), content)
  }
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      socket.push({ path: new URL(request.url).pathname, body: await request.text(), headers: [...request.headers] })
      return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'controller-response' } })
    } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/session-controller.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, ...(row.name === 'multiple-sdk-original-priority-batch-and-last-uuid' ? { CLAUDE_CODE_ENTRYPOINT: 'sdk-cli' } : {}), NODE_ENV: 'production', TZ: 'UTC', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin, ANTHROPIC_CUSTOM_HEADERS: 'x-client-request-id: original-fixture-trace', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin') }), stdin: new Blob([JSON.stringify({ ...row.input, origin: server.url.origin, stat: stats, runtime: { platform: process.platform, arch: process.arch, version: process.version } })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 35000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await saveEvidence(row.name + (go ? '-go' : '-source') + '-stderr.txt', stderr)
      await saveEvidence(row.name + (go ? '-go' : '-source') + '-process.json', JSON.stringify({ original: row, code, stdout, stderr, socket }, null, 2))
      if (go && row.pending) {
        expect(code).not.toBe(0)
        expect(stderr).toContain('dynamic SDK cancellation/admission')
        expect(socket).toHaveLength(0)
        return { pending: true }
      }
      if (go && code !== 0) {
        try { await saveEvidence(row.name + '-go-native-failure.json', await readFile(join(home, 'native-failed-controller.json'))) }
        catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
      }
      expect(code, stderr || stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(await readFile(join(home, 'controller-result.json'), 'utf8'))
      await saveEvidence(row.name + (go ? '-go' : '-source') + '-raw.json', JSON.stringify({ original: row, result, stdout, socket }, null, 2))
      if (!go) {
        expect(result.sourceOnly.capabilities.blocked).toEqual([])
        expect(result.sourceOnly.capabilities.secureReads).toEqual(['read'])
        expect(result.sourceOnly.exactAbsentPaths).toHaveLength(result.sourceOnly.capabilities.absentReads.length)
      }
      expect(socket).toHaveLength((row.input.sdk as unknown[]).length ? row.input.sdkPrelude ? 2 : 1 : 0)
      expect(result.observation.requests).toEqual(socket.map(item => item.body))
      return result.observation
    }
    finally {
      clearTimeout(timer)
    }
  }
  finally {
    child?.kill()
    if (child)
      await child.exited
    server.stop(true)
  }
}
for (const name of controllerCases('/placeholder').map(row => row.name))
  test((controllerCases('/placeholder').find(row => row.name === name)?.pending ? 'pending raw concurrent SDK Source trace + finite Go admission capability/no socket: ' : 'actual runHeadless → per-turn ask → accepted writer → first HTTP: ') + name, async () => {
    const home = await realpath(await mkdtemp(join(allocationRoot, 'session-controller-case-')))
    try {
      const row = controllerCases(home).find(value => value.name === name)!
      const source = await run(row, false), replay = await run(row, false)
      expect(replay).toEqual(source)
      if (name.startsWith('read-cache-hosted-')) {
        const count = name === 'read-cache-hosted-12-original-history' ? 12 : 101
        expect(source.activationHistory).toHaveLength(2 * count + 2)
        expect(source.cache).toHaveLength(Math.min(count, 100))
        expect(source.cache.map((value: {
          path: string
        }) => value.path)).toEqual(Array.from({ length: Math.min(count, 100) }, (_, index) => join(home, `file-${count - index - 1}.txt`)))
      }
      const go = await run(row, true)
      if (row.pending) {
        expect(go).toEqual({ pending: true })
      }
      else {
        expect(go).toEqual(source)
        const sourceWire = JSON.parse(await readFile(join(artifact, name + '-source-raw.json'), 'utf8')).socket
        const goWire = JSON.parse(await readFile(join(artifact, name + '-go-raw.json'), 'utf8')).socket
        const applicationHeaders = (values: Array<[
          string,
          string
        ]>) => values.filter(([header]) => !['host', 'accept-encoding', 'connection'].includes(header))
        expect(goWire.map((value: {
          headers: Array<[
            string,
            string
          ]>
        }) => applicationHeaders(value.headers))).toEqual(sourceWire.map((value: {
          headers: Array<[
            string,
            string
          ]>
        }) => applicationHeaders(value.headers)))
        await writeFile(join(artifact, name + '-native-transport-pending.json'), JSON.stringify({ sourceRaw: sourceWire, goRaw: goWire, pending: ['Host independently allocated loopback port', 'native Accept-Encoding/Connection defaults', 'compression/pooling/raw response metadata'] }, null, 2))
      }
    }
    finally {
      await rm(home, { recursive: true, force: true })
    }
  }, 120000)
test('actual original priority batch: five fresh native file/promise order observations', async () => {
  const home = await realpath(await mkdtemp(join(allocationRoot, 'session-controller-case-')))
  try {
    const row = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
    let baseline: unknown
    for (let index = 0; index < 5; index++) {
      const observation = await run(row, false)
      await writeFile(join(artifact, `multiple-sdk-fresh-source-${index}-raw.json`), await readFile(join(artifact, row.name + '-source-raw.json')))
      if (index === 0) baseline = observation
      else expect(observation).toEqual(baseline)
    }
  }
  finally { await rm(home, { recursive: true, force: true }) }
}, 120000)

test('actual Source native main cold/warm cleanup: original raw diagnostic observations', async () => {
  const home = await realpath(await mkdtemp(join(allocationRoot, 'session-controller-case-')))
  try {
    const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
    for (const warmCleanup of [false, true]) {
      const row = { ...original, input: { ...original.input, warmCleanup } }
      for (let index = 0; index < 5; index++) {
        await run(row, false)
        await writeFile(join(artifact, `multiple-sdk-original-${warmCleanup ? 'warm' : 'cold'}-cleanup-${index}-raw.json`), await readFile(join(artifact, row.name + '-source-raw.json')))
      }
    }
  }
  finally { await rm(home, { recursive: true, force: true }) }
}, 120000)

test('actual native main priority batch: own persisted resume and next human HTTP consumer', async () => {
  const home = await realpath(await mkdtemp(join(allocationRoot, 'session-controller-case-')))
  const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
  try {
    for (const timeline of ['original-clock', 'increasing-clock']) {
      const row = { ...original, input: { ...original.input, ...(timeline === 'increasing-clock' ? { nowMillis: 1767225600000 } : {}) } }
      const outcomes: any[] = []
      for (const profile of ['source-cold', 'source-warm', 'go']) {
        const go = profile === 'go'
        const first = { ...row, input: { ...row.input, ...(profile === 'source-warm' ? { warmCleanup: true } : {}) } }
        const accepted = await run(first, go)
        const followupSocket: any[] = []
        const followupWire = requestContextWire.replace('event: message_delta', 'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"FOLLOWUP_NATIVE_OK"}}\n\nevent: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\nevent: message_delta')
        const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
          followupSocket.push({ path: new URL(request.url).pathname, body: await request.text(), headers: [...request.headers] })
          return new Response(followupWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'followup-native-response' } })
        } })
        try {
          // Preserve this implementation's own initial output file. No Source
          // prepared transcript is copied into the Go continuation's private home.
          const input = { ...row.input, ...(timeline === 'increasing-clock' ? { nowMillis: 1767225601000 } : {}), nativeMain: true, sdk: [{ type: 'user', uuid: 'followup-native-id', message: { role: 'user', content: 'FOLLOWUP_NATIVE_HUMAN' } }], origin: server.url.origin, stat: {}, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
          const child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/session-controller.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, CLAUDE_CODE_ENTRYPOINT: 'sdk-cli', NODE_ENV: 'production', TZ: 'UTC', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin, ANTHROPIC_CUSTOM_HEADERS: 'x-client-request-id: original-fixture-trace', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin') }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
          const timeout = setTimeout(() => child.kill('SIGKILL'), 35000)
          try {
            const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
            const result = JSON.parse(await readFile(join(home, 'controller-result.json'), 'utf8'))
            await writeFile(join(artifact, `multiple-sdk-${timeline}-${profile}-own-resume-next-human-raw.json`), JSON.stringify({ original: input, accepted, result, stdout, stderr, code, socket: followupSocket }, null, 2))
            expect(code, stderr || stdout).toBe(0)
            expect(stderr).toBe('')
            expect(followupSocket).toHaveLength(1)
            expect(result.observation.requests).toEqual(followupSocket.map(value => value.body))
            const frames = (go ? result.nativeOnly.stream.output : stdout.trim().split('\n')).map((line: string) => JSON.parse(line))
            expect(frames.some((frame: any) => frame.type === 'result' && frame.subtype === 'success' && frame.result === 'FOLLOWUP_NATIVE_OK')).toBe(true)
            const body = JSON.parse(followupSocket[0].body)
            // The legacy timeline has a 2025 parent and a 2023 real clock;
            // the increasing timeline advances the actual clock to 2026.
            // Preserve accepted files and compare each real resume branch.
            expect(JSON.stringify(accepted.transcripts)).toContain('later original')
            expect(JSON.stringify(accepted.transcripts)).toContain('now original')
            expect(JSON.stringify(accepted.transcripts)).toContain('next original')
            expect(JSON.stringify(body.messages)).toContain('original user')
            expect(JSON.stringify(body.messages)).toContain('old answer')
            expect(JSON.stringify(body.messages)).toContain('FOLLOWUP_NATIVE_HUMAN')
            if (timeline === 'increasing-clock') {
              // Actual Source findLatestMessage uses a strict greater-than
              // comparison. The two accepted ASK leaves share a millisecond
              // in this legacy constant-clock profile, so resume keeps the
              // first user leaf. The advancing ASK test below covers normal
              // chronological acceptance without replacing this tie case.
              expect(JSON.stringify(body.messages)).toContain('later original')
              expect(JSON.stringify(body.messages)).not.toContain('now original')
              expect(JSON.stringify(body.messages)).not.toContain('next original')
            }
            const picker = go ? result.nativeOnly.stream.resumePicker : result.sourceOnly.resumePicker
            expect(picker).toHaveLength(1)
            expect(picker[0].sessionId).toBe(row.input.sid)
            expect(['later original', 'now original next original']).toContain(picker[0].firstPrompt)
            outcomes.push({ profile, body, activationHistory: result.observation.activationHistory, historySource: result.observation.historySource, transcripts: result.observation.transcripts, resumeFirstPrompt: picker[0].firstPrompt })
          }
          finally { clearTimeout(timeout); child.kill(); await child.exited }
        }
        finally { server.stop(true) }
      }
      // Physical byte order remains raw above. These are the next real request
      // and restored conversation consumers, not a sorted transcript projection.
      expect(outcomes[1].body).toEqual(outcomes[0].body)
      expect(outcomes[2].body).toEqual(outcomes[0].body)
      expect(outcomes[1].activationHistory).toEqual(outcomes[0].activationHistory)
      expect(outcomes[2].activationHistory).toEqual(outcomes[0].activationHistory)
      expect(outcomes[1].historySource).toEqual(outcomes[0].historySource)
      expect(outcomes[2].historySource).toEqual(outcomes[0].historySource)
      await writeFile(join(artifact, `multiple-sdk-${timeline}-own-resume-next-human-consumers.json`), JSON.stringify(outcomes, null, 2))
    }
  }
  finally { await rm(home, { recursive: true, force: true }) }
}, 120000)

test('actual native main advancing ASK clock: own persisted resume and next human HTTP consumer', async () => {
  const home = await realpath(await mkdtemp(join(allocationRoot, 'session-controller-case-')))
  const original = controllerCases(home).find(row => row.name === 'multiple-sdk-original-priority-batch-and-last-uuid')!
  try {
    for (const timeline of ['advancing-ask-clock']) {
      const row = { ...original, input: { ...original.input, nowMillis: 1767225600000, clockStepMillis: 1000 } }
      const outcomes: any[] = []
      for (const profile of ['source-cold', 'source-warm', 'go']) {
        const go = profile === 'go'
        const first = { ...row, input: { ...row.input, ...(profile === 'source-warm' ? { warmCleanup: true } : {}) } }
        const accepted = await run(first, go)
        const followupSocket: any[] = []
        const followupWire = requestContextWire.replace('event: message_delta', 'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"FOLLOWUP_NATIVE_OK"}}\n\nevent: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\nevent: message_delta')
        const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
          followupSocket.push({ path: new URL(request.url).pathname, body: await request.text(), headers: [...request.headers] })
          return new Response(followupWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'followup-native-response' } })
        } })
        try {
          // Preserve this implementation's own initial output file. No Source
          // prepared transcript is copied into the Go continuation's private home.
          const input = { ...row.input, nowMillis: 1767225603000, nativeMain: true, sdk: [{ type: 'user', uuid: 'followup-native-id', message: { role: 'user', content: 'FOLLOWUP_NATIVE_HUMAN' } }], origin: server.url.origin, stat: {}, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
          const child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/session-controller.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot, CLAUDE_CODE_ENTRYPOINT: 'sdk-cli', NODE_ENV: 'production', TZ: 'UTC', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin, ANTHROPIC_CUSTOM_HEADERS: 'x-client-request-id: original-fixture-trace', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin') }), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
          const timeout = setTimeout(() => child.kill('SIGKILL'), 35000)
          try {
            const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
            const result = JSON.parse(await readFile(join(home, 'controller-result.json'), 'utf8'))
            await writeFile(join(artifact, `multiple-sdk-${timeline}-${profile}-own-resume-next-human-raw.json`), JSON.stringify({ original: input, accepted, result, stdout, stderr, code, socket: followupSocket }, null, 2))
            expect(code, stderr || stdout).toBe(0)
            expect(stderr).toBe('')
            expect(followupSocket).toHaveLength(1)
            expect(result.observation.requests).toEqual(followupSocket.map(value => value.body))
            const frames = (go ? result.nativeOnly.stream.output : stdout.trim().split('\n')).map((line: string) => JSON.parse(line))
            expect(frames.some((frame: any) => frame.type === 'result' && frame.subtype === 'success' && frame.result === 'FOLLOWUP_NATIVE_OK')).toBe(true)
            const body = JSON.parse(followupSocket[0].body)
            // This additional profile advances the external clock at actual
            // ASK starts in 2026, after the immutable 2025 parent.
            // Preserve accepted files and compare each real resume branch.
            expect(JSON.stringify(accepted.transcripts)).toContain('later original')
            expect(JSON.stringify(accepted.transcripts)).toContain('now original')
            expect(JSON.stringify(accepted.transcripts)).toContain('next original')
            expect(JSON.stringify(body.messages)).toContain('original user')
            expect(JSON.stringify(body.messages)).toContain('old answer')
            expect(JSON.stringify(body.messages)).toContain('FOLLOWUP_NATIVE_HUMAN')
            if (timeline === 'advancing-ask-clock') {
              expect(JSON.stringify(body.messages)).toContain('later original')
              expect(JSON.stringify(body.messages)).toContain('now original')
              expect(JSON.stringify(body.messages)).toContain('next original')
            }
            const picker = go ? result.nativeOnly.stream.resumePicker : result.sourceOnly.resumePicker
            expect(picker).toHaveLength(1)
            expect(picker[0].sessionId).toBe(row.input.sid)
            expect(['later original', 'now original next original']).toContain(picker[0].firstPrompt)
            outcomes.push({ profile, body, activationHistory: result.observation.activationHistory, historySource: result.observation.historySource, transcripts: result.observation.transcripts, resumeFirstPrompt: picker[0].firstPrompt })
          }
          finally { clearTimeout(timeout); child.kill(); await child.exited }
        }
        finally { server.stop(true) }
      }
      // Physical byte order remains raw above. These are the next real request
      // and restored conversation consumers, not a sorted transcript projection.
      expect(outcomes[1].body).toEqual(outcomes[0].body)
      expect(outcomes[2].body).toEqual(outcomes[0].body)
      expect(outcomes[1].activationHistory).toEqual(outcomes[0].activationHistory)
      expect(outcomes[2].activationHistory).toEqual(outcomes[0].activationHistory)
      expect(outcomes[1].historySource).toEqual(outcomes[0].historySource)
      expect(outcomes[2].historySource).toEqual(outcomes[0].historySource)
      await writeFile(join(artifact, `multiple-sdk-${timeline}-own-resume-next-human-consumers.json`), JSON.stringify(outcomes, null, 2))
    }
  }
  finally { await rm(home, { recursive: true, force: true }) }
}, 120000)

function environment(home: string, origin: string) {
  return createSandboxedTestEnvironment(home, {
    CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1',
    CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot,
    NODE_ENV: 'production',
    TZ: 'UTC',
    ANTHROPIC_API_KEY: 'offline-fixture-key',
    ANTHROPIC_BASE_URL: origin,
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    USE_BUILTIN_RIPGREP: 'false',
    PATH: join(home, 'bin'),
  })
}
async function childResult(home: string, input: Record<string, unknown>, go: boolean, fixture = 'session-controller.fixture.ts') {
  const child = Bun.spawn(go ? [executable] : [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity', fixture)], {
    cwd: home,
    env: environment(home, input.origin as string),
    stdin: new Blob([JSON.stringify(input)]),
    stdout: 'pipe',
    stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 35000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    return { code, stdout, stderr }
  }
  finally {
    clearTimeout(timer)
    child.kill()
    await child.exited
  }
}
for (const mode of ['existing', 'missing', 'delete', 'replace']) {
  test(`actual adopted Project queue/drain helper, separate from headless: ${mode}`, async () => {
    const home = await realpath(await mkdtemp(join(allocationRoot, 'controller-writer-')))
    try {
      const row = controllerCases(home)[0]!
      const originalLog = Object.keys(row.files).find(path => path.endsWith('.jsonl'))!
      if (mode === 'missing')
        delete row.files[originalLog]
      const runHelper = async (go: boolean) => {
        const stats = await initialize(row)
        const output = await childResult(home, { ...row.input, writerOnly: mode, origin: 'http://127.0.0.1:1', stat: stats }, go, 'session-controller-writer.fixture.ts')
        await writeFile(join(artifact, `adopt-${mode}-${go ? 'go' : 'source'}-raw.json`), JSON.stringify({ original: row, output }, null, 2))
        expect(output.code, output.stderr).toBe(0)
        expect(output.stderr).toBe('')
        const result = JSON.parse(output.stdout)
        if (!go)
          expect(result.capabilities.blocked).toEqual([])
        return result.observed
      }
      const source = await runHelper(false)
      expect(await runHelper(false)).toEqual(source)
      expect(await runHelper(true)).toEqual(source)
    }
    finally {
      await rm(home, { recursive: true, force: true })
    }
  }, 120000)
}
for (const name of ['config-leaf', 'known-input-leaf', 'known-input-directory', 'known-output-leaf', 'prepared-history']) {
  test(`before-import original boundary guard preserves external sentinel: ${name}`, async () => {
    const home = await realpath(await mkdtemp(join(allocationRoot, 'controller-guard-')))
    const external = await realpath(await mkdtemp(join(allocationRoot, 'controller-sentinel-')))
    const sentinel = join(external, 'sentinel')
    await writeFile(sentinel, 'external sentinel must stay unchanged')
    try {
      const row = controllerCases(home)[0]!
      const stats = await initialize(row)
      const input: Record<string, unknown> = { ...row.input, origin: 'http://127.0.0.1:1', stat: stats }
      if (name === 'config-leaf') {
        await rm(join(home, '.claude'), { recursive: true })
        await symlink(external, join(home, '.claude'))
      }
      else if (name === 'known-input-leaf') {
        const path = join(home, 'original-known-input')
        await symlink(sentinel, path)
        stats[path] = { birth: 0, modified: 0, size: 1 }
      }
      else if (name === 'known-input-directory') {
        await symlink(external, join(home, 'original-known-directory'))
        stats[join(home, 'original-known-directory', 'sentinel')] = { birth: 0, modified: 0, size: 1 }
      }
      else if (name === 'known-output-leaf') {
        await symlink(sentinel, join(home, 'controller-result.json'))
      }
      else
        input.preparedHistory = []
      for (const go of [false, true]) {
        const output = await childResult(home, input, go)
        await writeFile(join(artifact, `guard-${name}-${go ? 'go' : 'source'}-raw.json`), JSON.stringify(output, null, 2))
        expect(output.code).not.toBe(0)
        expect(output.stdout).toBe('')
        expect(output.stderr).not.toContain('/src/QueryEngine')
        expect(output.stderr).not.toContain('/src/cli/print')
        expect(await readFile(sentinel, 'utf8')).toBe('external sentinel must stay unchanged')
      }
    }
    finally {
      await rm(home, { recursive: true, force: true })
      await rm(external, { recursive: true, force: true })
    }
  }, 120000)
}
for (const name of ['requests', 'transcripts', 'boundaryOrder', 'uuidChecks', 'cache', 'appState', 'snapshotRecords']) {
  test(`strict actual observation comparator rejects ${name} mutant`, async () => {
    const selected = name === 'snapshotRecords' ? 'checkpoint-restore-with-no-turn-does-not-materialize-new-writer' : name === 'cache' ? 'read-cache-original-lines-lone-and-reminder' : 'sid-actual-headless-first-http'
    const value = JSON.parse(await readFile(join(artifact, `${selected}-source-raw.json`), 'utf8')).result.observation
    const mutant = structuredClone(value)
    if (name === 'requests')
      mutant.requests[0] += ' '
    else if (name === 'transcripts') {
      const file = Object.keys(mutant.transcripts)[0]!
      mutant.transcripts[file] += 'mutated persisted bytes\n'
    }
    else if (name === 'boundaryOrder')
      mutant.boundaryOrder.reverse()
    else if (name === 'uuidChecks')
      mutant.uuidChecks[0].exists = !mutant.uuidChecks[0].exists
    else if (name === 'cache')
      mutant.cache[0].value.content += 'mutation'
    else if (name === 'appState')
      mutant.appState.FileHistory.snapshotSequence++
    else
      mutant.snapshotRecords[0].isSnapshotUpdate = !mutant.snapshotRecords[0].isSnapshotUpdate
    expect(() => expect(mutant).toEqual(value)).toThrow()
  })
}
