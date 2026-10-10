import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'
import type { TimeoutObservation } from './recovery-timeout-oracle'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string

beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-parity-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'claude-haha.exe' : 'claude-haha')
  const child = Bun.spawn(['go', 'build', '-o', executable, './cmd/claude-haha'], {
    cwd: resolve(rootDir, 'backend'),
    env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally {
    clearTimeout(timer)
  }
}, 190_000)

afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Fixture = {
  args: string[]
  input?: string[]
  env?: Record<string, string>
  responses?: Array<{ status?: number; body?: unknown; rawBody?: string; delayMs?: number; bodyDelayMs?: number }>
  systemFile?: string
  expectedStderr?: string
  expectedRequests?: number
  expectedLastMessages?: unknown[]
}

const response = { id: 'msg_fixture', type: 'message', role: 'assistant', model: 'fixture-flash', content: [{ type: 'text', text: '中文 😀 <&>' }, { type: 'thinking', thinking: 'private' }, { type: 'text', text: 'second line' }], stop_reason: 'end_turn', stop_sequence: null, usage: { input_tokens: 10, output_tokens: 5 }, unknown: { preserve: true } }

function delayedJSONResponse(body: string, delayMs: number, status = 200): Response {
  const bytes = new TextEncoder().encode(body)
  let cancelled = false
  return new Response(new ReadableStream<Uint8Array>({
    async start(controller) {
      // Flush headers with a real first body fragment before the timeout, then
      // keep the JSON document incomplete beyond the configured request timer.
      controller.enqueue(bytes.slice(0, 1))
      await Bun.sleep(delayMs)
      if (!cancelled) {
        controller.enqueue(bytes.slice(1))
        controller.close()
      }
    },
    cancel() { cancelled = true },
  }), { status, headers: { 'content-type': 'application/json' } })
}

// These are literal HTTP bodies, not Response.json objects: parsing them first
// would hide double rounding, overflow, duplicate keys, and lone UTF16 units.
const rawHttpErrors: Array<[string, string, string]> = [
  ['zero message', '{"message":0,"tag":"fallback"}', '400 {"message":0,"tag":"fallback"}'],
  ['false message', '{"message":false,"tag":"fallback"}', '400 {"message":false,"tag":"fallback"}'],
  ['empty string message', '{"message":"","tag":"fallback"}', '400 {"message":"","tag":"fallback"}'],
  ['null message', '{"message":null,"tag":"fallback"}', '400 {"message":null,"tag":"fallback"}'],
  ['underflow message', '{"message":1e-400,"tag":"fallback"}', '400 {"message":0,"tag":"fallback"}'],
  ['overflow message', '{"message":1e400,"tag":"fallback"}', '400 null'],
  ['escaped Unicode message', '{"message":"\\u4e2d\\u6587 \\ud83d\\ude00 \\n\\u2028"}', '400 中文 😀 \n\u2028'],
  ['lone UTF16 message', '{"message":"A\\ud800B\\udfffC"}', '400 A\ufffdB\ufffdC'],
  ['object message with duplicate and numeric fields', '{"message":{"2":"two","z":1,"1":"one","z":2,"integer":9007199254740993,"huge":1e400,"tiny":1e-400}}', '400 {"1":"one","2":"two","z":2,"integer":9007199254740992,"huge":null,"tiny":0}'],
  ['top false body', ' false ', '400  false '],
  ['top zero body', ' 0 ', '400  0 '],
  ['top null body', ' null ', '400  null '],
  ['top empty string body', ' "" ', '400  "" '],
  ['top overflow body', ' 1e400 ', '400 null'],
  ['non JSON body', 'plain failure\n中文 😀 <&>', '400 plain failure\n中文 😀 <&>'],
  ['empty body', '', '400 status code (no body)'],
]

async function record(kind: 'typescript' | 'go', fixture: Fixture): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), `go-parity-${kind}-`))
  const trace: TraceEvent[] = []
  let responseIndex = 0
  const server = Bun.serve({
    hostname: '127.0.0.1', port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname
      if (path !== '/anthropic/v1/messages') return new Response('unexpected provider path', { status: 404 })
      const body = await request.text()
      trace.push({ kind: 'model-request', value: { method: request.method, path, body: JSON.parse(body), bodySemantics: canonicalJsonSource(body), authentication: { apiKey: request.headers.get('x-api-key'), authorization: request.headers.get('authorization'), version: request.headers.get('anthropic-version') } } })
      const next = fixture.responses?.[responseIndex++] ?? { body: response }
      if (next.delayMs !== undefined) await Bun.sleep(next.delayMs)
      if (next.bodyDelayMs !== undefined) return delayedJSONResponse(next.rawBody ?? JSON.stringify(next.body), next.bodyDelayMs, next.status ?? 200)
      if (next.rawBody !== undefined) return new Response(next.rawBody, { status: next.status ?? 200, headers: { 'content-type': 'application/json' } })
      return Response.json(next.body, { status: next.status ?? 200 })
    },
  })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    const args = [...fixture.args]
    if (fixture.systemFile !== undefined) {
      const path = join(home, 'system.txt')
      await writeFile(path, fixture.systemFile)
      args.unshift('--system-prompt-file', path)
    }
    const env = createSandboxedTestEnvironment(home, {
      ANTHROPIC_API_KEY: 'fixture-key', ANTHROPIC_MODEL: 'fixture-flash',
      ANTHROPIC_BASE_URL: `http://127.0.0.1:${server.port}/anthropic/v1/`, API_TIMEOUT_MS: '2000',
      CLAUDE_CODE_FORCE_RECOVERY_CLI: '1',
      ...fixture.env,
    })
    child = Bun.spawn(kind === 'go' ? [executable, ...args] : [process.execPath, '--no-env-file', join(rootDir, 'src/localRecoveryCli.ts'), ...args], {
      cwd: home, env, stdin: 'pipe', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 10_000)
    try {
      for (const chunk of fixture.input ?? []) {
        child.stdin.write(chunk)
        await child.stdin.flush()
      }
      child.stdin.end()
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      trace.push({ kind: 'stdio', value: { stdout, stderr, exitCode } })
      return trace
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill()
    server.stop(true)
    await rm(home, { recursive: true, force: true })
  }
}

const fixtures: Array<[string, Fixture]> = [
  ['help without credentials', { args: ['--help'], env: { ANTHROPIC_API_KEY: '', ANTHROPIC_MODEL: '' } }],
  ['version alias', { args: ['-V'] }],
  ['required prompt', { args: ['--print'] }],
  ['required authentication', { args: ['-p', 'hello'], env: { ANTHROPIC_API_KEY: '' } }],
  ['required model', { args: ['-p', 'hello'], env: { ANTHROPIC_MODEL: '' } }],
  ['text and positional joining', { args: ['-p', 'hello', '世界'] }],
  ['fragmented Unicode stdin', { args: ['--print'], input: ['\ufeff多行\n', '中文 😀\n'] }],
  ['JSON including unknown response fields', { args: ['-p', '--output-format', 'json', 'hello'] }],
  ['JSON raw escaped Unicode and UTF16', { args: ['-p', '--output-format', 'json', 'hello'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":"\\u4e2d\\u6587\\ud83d\\ude00 <&> \\u2028 \\n"}],"high":"\\ud800","low":"\\udfff","slash":"\\/"}' }] }],
  ['JSON raw literal double precision and exponent extremes', { args: ['-p', '--output-format', 'json', 'hello'], responses: [{ rawBody: '{"id":"msg_fixture","content":[],"integer":9007199254740993,"large":1e400,"negativeLarge":-1e400,"tiny":1e-400,"negativeZero":-0,"scientific":1e21}' }] }],
  ['JSON raw numeric key enumeration and duplicate keys', { args: ['-p', '--output-format', 'json', 'hello'], responses: [{ rawBody: '{"2":"two","z":1,"01":"leading-zero","1":"one","4294967295":"non-index","z":2,"0":"zero","\\u0033":"three","id":"msg_fixture","content":[]}' }] }],
  ['JSON raw nested arrays objects and escaped duplicate keys', { args: ['-p', '--output-format', 'json', 'hello'], responses: [{ rawBody: '{"id":"msg_fixture","content":[],"nested":[{"a":1,"\\u0061":2},[{},[],null,true,false]],"\\ud800":"lone-key"}' }] }],
  ['JSON raw malformed typed text stays unchanged', { args: ['-p', '--output-format', 'json', 'hello'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":null},{"type":"text","text":3},{"type":"text","text":{"proxy":true}},{"type":"text","text":[1,null,true]},{"type":"text"}]}' }] }],
  ['text raw malformed typed values use Array join coercion', { args: ['-p', 'hello'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":null},{"type":"text","text":1e400},{"type":"text","text":{"proxy":true}},{"type":"text","text":[1,null,true]},{"type":"text"},{"type":"thinking","thinking":"private"}]}' }] }],
  ['both authentication headers', { args: ['-p', 'hello'], env: { ANTHROPIC_AUTH_TOKEN: 'fixture-token' } }],
  ['auth token and default model fallback', { args: ['-p', '--model', ''], env: { ANTHROPIC_API_KEY: '', ANTHROPIC_AUTH_TOKEN: 'fixture-token', ANTHROPIC_DEFAULT_SONNET_MODEL: 'fallback-model', ANTHROPIC_MODEL: '' }, input: ['hello'] }],
  ['repeat flags and legacy unknown flag behavior', { args: ['--print', '--model', 'first', '--model', 'last', '--bare', '--dangerously-skip-permissions', '--unknown', 'hello'] }],
  ['system plus append', { args: ['-p', '--system-prompt', 'base', '--append-system-prompt', 'append', 'hello'] }],
  ['empty system suppresses append', { args: ['-p', '--system-prompt', '', '--append-system-prompt', 'ignored', 'hello'] }],
  ['system file and append', { args: ['-p', '--append-system-prompt', 'append', 'hello'], systemFile: 'from file\n中文' }],
  ['interactive conversation and clear', { args: [], input: ['\nfirst\nsecond\n/clear\nafter\n/exit\n'] }],
  ['interactive lone UTF16 text retains next request history', { args: [], input: ['first\nsecond\n/exit\n'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":"A\\ud800B\\udfffC"}]}' }, { body: response }] }],
  ['interactive split surrogate text blocks retain next request history', { args: [], input: ['first\nsecond\n/exit\n'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":"\\ud83d"},{"type":"text","text":"\\ude00"}]}' }, { body: response }] }],
  ['interactive surrogate arrays retain next request history', { args: [], input: ['first\nsecond\n/exit\n'], responses: [{ rawBody: '{"id":"msg_fixture","content":[{"type":"text","text":["\\ud83d",null,"\\ude00",["\\ud800",[],"\\udfff"]]}]}' }, { body: response }] }],
  ['interactive error keeps failed user input in history', { args: [], input: ['first\nfailed\nthird\n/quit\n'], responses: [{ body: response }, { status: 500, body: { error: { type: 'api_error', message: 'fixture failure' } } }, { body: response }] }],
  ...rawHttpErrors.map(([name, rawBody, message]): [string, Fixture] => [
    `interactive HTTP error ${name} preserves next request history`,
    { args: [], input: ['failed\nnext\n/exit\n'], responses: [{ status: 400, rawBody }, { body: response }], expectedStderr: `error: ${message}\n`, expectedRequests: 2, expectedLastMessages: [{ role: 'user', content: 'failed' }, { role: 'user', content: 'next' }] },
  ]),
  ...[
    ['NaN', 'NaN', 'timeout must be an integer'],
    ['negative', '-1', 'timeout must be a positive integer'],
    ['NEL prefix', '\u00851000', 'timeout must be an integer'],
  ].map(([name, input, message]): [string, Fixture] => [
    `interactive timeout ${name} rejects each turn before HTTP`,
    { args: [], input: ['failed\nnext\n/exit\n'], env: { API_TIMEOUT_MS: input! }, expectedStderr: `error: ${message}\nerror: ${message}\n`, expectedRequests: 0 },
  ]),
  ['interactive timeout BOM prefix permits delayed responses and history', { args: [], input: ['first\nsecond\n/exit\n'], env: { API_TIMEOUT_MS: '\ufeff1000' }, responses: [{ body: response, delayMs: 80 }, { body: response, delayMs: 80 }] }],
  ['interactive timeout ends at response headers and permits delayed body and history', { args: [], input: ['first\nsecond\n/exit\n'], env: { API_TIMEOUT_MS: '100' }, responses: [{ body: response, bodyDelayMs: 250 }, { body: response, bodyDelayMs: 250 }], expectedStderr: '', expectedRequests: 2, expectedLastMessages: [{ role: 'user', content: 'first' }, { role: 'assistant', content: '中文 😀 <&>\nsecond line' }, { role: 'user', content: 'second' }] }],
]

for (const [name, fixture] of fixtures) {
  test(`Recovery TS self replay and Go parity: ${name}`, async () => {
    const expected = await record('typescript', fixture)
    if (fixture.expectedStderr !== undefined) {
      const stdio = expected.find(event => event.kind === 'stdio')!.value as { stderr: string; exitCode: number }
      expect(stdio.stderr).toBe(fixture.expectedStderr)
      expect(stdio.exitCode).toBe(0)
      const requests = expected.filter(event => event.kind === 'model-request').map(event => event.value as { body: { messages: unknown[] } })
      if (fixture.expectedRequests !== undefined) expect(requests).toHaveLength(fixture.expectedRequests)
      if (fixture.expectedLastMessages !== undefined) expect(requests.at(-1)!.body.messages).toEqual(fixture.expectedLastMessages)
    }
    assertTraceEqual(expected, await record('typescript', fixture))
    assertTraceEqual(expected, await record('go', fixture))
  }, 35_000)
}

type TimeoutFixture = { input: string; parsed: string; error?: string; fires?: boolean; warning?: string; bodyDelayMs?: number }
const timeoutFixtures: Array<[string, TimeoutFixture]> = [
  ['NaN', { input: 'NaN', parsed: 'NaN', error: 'timeout must be an integer' }],
  ['negative', { input: '-1', parsed: '-1', error: 'timeout must be a positive integer' }],
  ['zero', { input: '0', parsed: '0', fires: true }],
  ['radix ten hex form', { input: '0x20', parsed: '0', fires: true }],
  ['32 bit timer boundary', { input: '2147483647', parsed: '2147483647' }],
  ['32 bit timer overflow', { input: '2147483648', parsed: '2147483648', fires: true, warning: 'TimeoutOverflowWarning: 2147483648 does not fit into a 32-bit signed integer.\nTimeout duration was set to 1.\n' }],
  ['large finite parsed integer', { input: '999999999999999999999999999999999999999999999999', parsed: '1e+48', fires: true, warning: 'TimeoutOverflowWarning: 1000000000000000000000000000000000000000000000000 does not fit into a 32-bit signed integer.\nTimeout duration was set to 1.\n' }],
  ['parsed Infinity rejected before timer', { input: '9'.repeat(400), parsed: 'Infinity', error: 'timeout must be an integer' }],
  ['BOM prefix', { input: '\ufeff1000', parsed: '1000' }],
  ['NEL prefix', { input: '\u00851000', parsed: 'NaN', error: 'timeout must be an integer' }],
  ['default empty timeout', { input: '', parsed: '600000' }],
  ['timer stops at response headers before delayed body', { input: '100', parsed: '100', bodyDelayMs: 250 }],
]

async function recordTimeoutOracle(fixture: TimeoutFixture) {
  const home = await mkdtemp(join(tmpdir(), 'recovery-timeout-oracle-'))
  let receivedRequests = 0
  const server = Bun.serve({
    hostname: '127.0.0.1', port: 0,
    async fetch() {
      receivedRequests++
      if (fixture.bodyDelayMs !== undefined) return delayedJSONResponse('{"content":[{"type":"text","text":"delayed success"}]}', fixture.bodyDelayMs)
      await Bun.sleep(80)
      return Response.json({ content: [{ type: 'text', text: 'delayed success' }] })
    },
  })
  const child = Bun.spawn([process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/recovery-timeout-oracle.ts')], {
    cwd: home,
    env: createSandboxedTestEnvironment(home, { API_TIMEOUT_MS: fixture.input, ANTHROPIC_BASE_URL: `http://127.0.0.1:${server.port}` }),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 10_000)
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(exitCode, stderr).toBe(0)
    const { elapsedMs, ...contract } = JSON.parse(stdout) as TimeoutObservation
    // The exact warning body is observable behavior. Only source-path/line
    // stacks and elapsed time are explicit nonsemantic diagnostic metadata.
    const lines = stderr.match(/[^\n]*\n|[^\n]+$/g) ?? []
    const stackStart = lines.findIndex(line => /^ {6}at /.test(line))
    const stackMetadata = stackStart < 0 ? [] : lines.slice(stackStart)
    if (stackMetadata.some(line => !/^ {6}at /.test(line) && line !== '\n')) throw new Error(`Unexpected runtime warning stack: ${stderr}`)
    const warning = stackStart < 0 ? stderr : lines.slice(0, stackStart).join('')
    return { trace: [{ kind: 'state', value: { ...contract, warning } }] satisfies TraceEvent[], metadata: { stderr, stackMetadata, elapsedMs, receivedRequests } }
  } finally {
    clearTimeout(timer)
    child.kill()
    server.stop(true)
    await rm(home, { recursive: true, force: true })
  }
}

for (const [name, fixture] of timeoutFixtures) {
  test(`Recovery pinned SDK timeout timer observation: ${name}`, async () => {
    const expected = await recordTimeoutOracle(fixture)
    expect(expected.trace[0]!.value).toEqual({
      input: fixture.input, parsed: fixture.parsed, clientTimeout: fixture.parsed,
      scheduled: fixture.error ? [] : [fixture.parsed], fired: fixture.fires ? [fixture.parsed] : [],
      fetchCalls: fixture.error ? 0 : 1, aborts: fixture.fires ? 1 : 0,
      outcome: fixture.error ? { name: 'Error', message: fixture.error } : fixture.fires ? { name: 'Error', message: 'Request timed out.' } : { text: 'delayed success' },
      warning: fixture.warning ?? '',
    })
    if (fixture.warning) expect(expected.metadata.stackMetadata.length).toBeGreaterThan(0)
    assertTraceEqual(expected.trace, (await recordTimeoutOracle(fixture)).trace)
  }, 25_000)
}
