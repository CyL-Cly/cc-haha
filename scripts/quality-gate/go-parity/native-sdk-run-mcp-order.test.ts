import { expect, test } from 'bun:test'
import { mkdtemp, readFile, rm, mkdir, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { requestContextWire } from './request-context-fixtures'

test('actual runHeadless awaits original SDK MCP handshake before dequeue and command lifecycle', async () => {
  const home = await mkdtemp(join(tmpdir(), 'sdk-run-mcp-order-'))
  const requests: string[] = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    requests.push(await request.text())
    return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'sdk-order-response' } })
  } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    await writeFile(join(home, '.claude', '.claude.json'), '{"userID":"private-sdk-order-device"}')
    child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', fileURLToPath(new URL('./native-sdk-run-mcp-order.fixture.ts', import.meta.url))], {
      cwd: home,
      env: createSandboxedTestEnvironment(home, {
        CC_HAHA_SDK_RUN_MCP_ORDER: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', TZ: 'UTC',
        ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin,
        CLAUDE_CODE_SIMPLE: '1', DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
        CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_MAX_RETRIES: '0',
        CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin'),
      }),
      stdin: new Blob([JSON.stringify({ origin: server.url.origin })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr || stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(await readFile(join(home, 'sdk-run-mcp-order-result.json'), 'utf8'))
      const artifacts = fileURLToPath(new URL('../../../artifacts/go-parity/sdk-run-mcp-order/', import.meta.url))
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, 'source-raw.json'), JSON.stringify({ result, requests, stdout, stderr, code }, null, 2))
      expect(result.code).toBe(0)
      expect(result.capabilities.blocked).toEqual([])
      expect(result.observations).toEqual([{ phase: 'SDK initialize', dequeues: 0, starts: 0, queued: [{ value: 'SDK_ORDER_HUMAN', uuid: 'order-user' }] }])
      expect(result.phases).toEqual(['SDK initialize', 'dequeue'])
      expect(result.dequeues).toBe(1)
      expect(result.starts).toBe(1)
      expect(requests).toHaveLength(1)
      expect(JSON.stringify(JSON.parse(requests[0]!).messages)).toContain('SDK_ORDER_HUMAN')
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill()
    if (child) await child.exited
    server.stop(true)
    await rm(home, { recursive: true, force: true })
  }
}, 35000)
