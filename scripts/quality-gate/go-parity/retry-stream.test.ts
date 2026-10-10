import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { assertTraceEqual, type TraceEvent } from './trace'

const root = fileURLToPath(new URL('../../../', import.meta.url)), temporaryRoot = tmpdir()
let buildHome: string, executable: string
type Failure = { kind: 'api' | 'syntax' | 'native' | 'nil'; sdkClass?: string; message?: string; code?: string; payload?: unknown; cause?: Failure; selfCause?: boolean }
const wrap = (depth: number, leaf: Failure): Failure => depth ? { kind: 'native', message: 'outer', cause: wrap(depth - 1, leaf) } : leaf
const nested = (depth: number, leaf: unknown): unknown => depth ? { error: nested(depth - 1, leaf) } : leaf
const api = (message: string, payload?: unknown, cause?: Failure): Failure => ({ kind: 'api', message, payload, cause })
const failures: Failure[] = [
  { kind: 'nil' }, { kind: 'native', message: 'ECONNRESET is prose' }, { kind: 'native', selfCause: true },
  ...['ECONNRESET', 'ECONNABORTED', 'EPIPE', 'UND_ERR_SOCKET', 'ERR_STREAM_PREMATURE_CLOSE', 'ETIMEDOUT', 'CERT_HAS_EXPIRED', '', 'econnreset'].map(code => ({ kind: 'native' as const, code, message: 'disconnect' })),
  wrap(4, { kind: 'native', code: 'EPIPE' }), wrap(5, { kind: 'native', code: 'EPIPE' }),
  { kind: 'native', code: '', cause: { kind: 'native', code: 'EPIPE' } },
  { kind: 'native', code: 'unknown-first', cause: { kind: 'native', code: 'EPIPE' } },
  ...['', 'api_error', '{"type":"api_error"}', '{"type": "api_error"}', '500 {"type":"overloaded_error"}'].map(message => api(message)),
  { kind: 'syntax', message: '{"type":"api_error"}' }, { kind: 'native', message: '{"type":"api_error"}' },
  ...['APIError', 'APIUserAbortError', 'APIConnectionError', 'APIConnectionTimeoutError', 'BadRequestError', 'AuthenticationError', 'PermissionDeniedError', 'NotFoundError', 'ConflictError', 'UnprocessableEntityError', 'RateLimitError', 'InternalServerError', 'AnthropicError', 'Error'].map(sdkClass => ({ kind: 'api' as const, sdkClass, message: '{"type":"api_error"}' })),
  ...['cyber_policy', 'content_policy', 'content_policy_violation', 'other'].map(code => ({ kind: 'native' as const, code, cause: { kind: 'native' as const, code: 'ECONNRESET' } })),
  ...[null, [], { code: 'cyber_policy' }, { error: { code: 'content_policy' } }, { response: { originalError: { cause: { code: 'content_policy_violation' } } } },
    { code: 123, error: { code: 'cyber_policy' } }, { message: '429\u00a0{"code":"cyber_policy"}' }, { message: '\ufeff{"code":"content_policy"}' },
    nested(11, { code: 'cyber_policy' }), nested(12, { code: 'cyber_policy' }), { message: 'cyber_policy is prose' },
  ].map(payload => api('{"type":"api_error"}', payload, { kind: 'native', code: 'EPIPE' })),
  ...['content_policy is prose', '{"code":"cyber_policy"}', '429 {"error":{"code":"content_policy"}}',
    '429\ufeff\u00a0{"code":"cyber_policy"}', '\u0085{"code":"cyber_policy"}', '{"code":"cyber_policy"} {}', '{invalid JSON', '{"message":"cyber_policy"}',
  ].map(message => ({ kind: 'native' as const, message, code: 'ECONNRESET' })),
  wrap(12, { kind: 'native', code: 'cyber_policy' }), wrap(13, { kind: 'native', code: 'cyber_policy' }),
  api('outer', undefined, { kind: 'native', code: 'ECONNRESET' }), api('{"type":"api_error"}', undefined, { kind: 'native', message: '{"code":"content_policy"}' }),
]
const trace = (value: unknown): TraceEvent[] => [{ kind: 'state', value: { profile: 'retry-stream', value } }]

beforeAll(async () => {
  buildHome = await mkdtemp(join(temporaryRoot, 'retry-stream-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'retry-stream.exe' : 'retry-stream')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/retrypolicy/testdata/fixture'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180000)
  try { const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()]); expect(code, stderr).toBe(0) } finally { clearTimeout(timer) }
}, 190000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function run(kind: 'typescript' | 'go', guard?: 'home' | 'config' | 'cwd', profile = 'stream') {
  const home = await mkdtemp(join(temporaryRoot, 'retry-stream-case-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    const sentinel = join(home, 'sentinel.json')
    await writeFile(sentinel, '{"protected":"unchanged"}')
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_RETRY_DELAY_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: temporaryRoot })
    if (guard === 'config') env.CLAUDE_CONFIG_DIR = join(home, 'wrong-config')
    const fixture = join(root, `scripts/quality-gate/go-parity/retry-${profile}.fixture.ts`)
    const loader = join(home, 'tripwire.ts')
    if (guard) await writeFile(loader, `import { plugin } from 'bun'
plugin({ name: 'retry-production-tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_IMPORT_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })
await import(${JSON.stringify(fixture)})`)
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', guard ? loader : fixture], {
      cwd: guard === 'cwd' ? root : home, env, stdin: new Blob([JSON.stringify({ home: guard === 'home' ? join(home, 'missing') : home, failures, delays: [] })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 20000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      if (guard) {
        expect(code).not.toBe(0); expect(stdout).toBe(''); expect(stderr).toContain('disposable HOME/config before')
        expect(await readFile(sentinel, 'utf8')).toBe('{"protected":"unchanged"}')
        return null
      }
      expect(code, stderr).toBe(0); expect(stderr).toBe('')
      const parsed = JSON.parse(stdout)
      if (kind === 'typescript') { expect(parsed.capabilities).toEqual({ blocked: [], secureReads: [] }); delete parsed.capabilities }
      return parsed
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}

test('actual Source SDK/native fault classifiers replay without external capabilities', async () => {
  const source = await run('typescript')
  assertTraceEqual(trace(source), trace(await run('typescript')))
  const artifacts = join(root, 'artifacts/go-parity/retry-stream')
  await mkdir(artifacts, { recursive: true })
  await writeFile(join(artifacts, 'source.json'), JSON.stringify({ failures, source }, null, 2))
}, 45000)
test('independent Go fault and committed-tool/abort retry decisions match Source', async () => {
  assertTraceEqual(trace(await run('typescript')), trace(await run('go')))
}, 45000)
for (const profile of ['delay', 'stream']) for (const boundary of ['home', 'config', 'cwd'] as const) test(`${profile}: reject ${boundary} before business imports/execution`, async () => {
  await run('typescript', boundary, profile); await run('go', boundary, profile)
}, 45000)
test('comparison detects every semantic retry decision change', async () => {
  const source = await run('typescript')
  for (const field of ['policy', 'api', 'transport', 'code', 'states']) {
    const mutant = structuredClone(source)
    mutant.outcomes[0][field] = field === 'code' ? 'EPIPE' : field === 'states' ? [true, ...mutant.outcomes[0].states.slice(1)] : true
    expect(() => assertTraceEqual(trace(source), trace(mutant))).toThrow()
  }
}, 45000)
