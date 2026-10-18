import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

function wire(content: Record<string, unknown>, stop: string) {
  const event = (name: string, value: unknown) => `event: ${name}\ndata: ${JSON.stringify(value)}\n\n`
  const tool = content.type === 'tool_use'
  return event('message_start', { type: 'message_start', message: { id: 'owned-sdk-analytics', type: 'message', role: 'assistant', content: [], model: 'claude-sonnet-4-5-20250929', stop_reason: null, stop_sequence: null, usage: { input_tokens: 3, output_tokens: 0 } } }) +
    event('content_block_start', { type: 'content_block_start', index: 0, content_block: tool ? { ...content, input: {} } : { type: 'text', text: '' } }) +
    event('content_block_delta', { type: 'content_block_delta', index: 0, delta: tool ? { type: 'input_json_delta', partial_json: JSON.stringify(content.input) } : { type: 'text_delta', text: content.text } }) +
    event('content_block_stop', { type: 'content_block_stop', index: 0 }) +
    event('message_delta', { type: 'message_delta', delta: { stop_reason: stop, stop_sequence: null }, usage: { output_tokens: 4 } }) +
    event('message_stop', { type: 'message_stop' })
}

test('whole original SDK environment and get_settings retain actual file analytics exporter', async () => {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'sdk-analytics-environment-')))
  const models: unknown[] = [], exports: Array<{ body: any; headers: Record<string, string> }> = []
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    const url = new URL(request.url)
    if (request.method === 'HEAD') return new Response(null, { status: 204 })
    if (request.method === 'POST' && url.pathname === '/events') { exports.push({ body: await request.json(), headers: Object.fromEntries(request.headers) }); return Response.json({ accepted: true }) }
    if (request.method !== 'POST' || url.pathname !== '/v1/messages') throw new Error('Unowned SDK analytics HTTP primitive')
    models.push(await request.json())
    const content = models.length === 1 ? { type: 'tool_use', id: 'owned-sdk-read', name: 'Read', input: { file_path: join(home, 'workspace', 'observed.txt') } } : { type: 'text', text: 'SDK_ANALYTICS_DONE' }
    return new Response(wire(content, models.length === 1 ? 'tool_use' : 'end_turn'), { headers: { 'content-type': 'text/event-stream', 'request-id': 'owned-sdk-analytics' } })
  } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, 'bin'))
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_SDK_ANALYTICS_ENVIRONMENT: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', USER_TYPE: 'external', ANTHROPIC_API_KEY: 'fake-sdk-analytics-key', ANTHROPIC_BASE_URL: peer.url.origin, CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin') })
    child = Bun.spawn([process.env.CC_HAHA_SOURCE_BUN ?? process.execPath, '--no-env-file', fileURLToPath(new URL('./native-sdk-analytics-environment.fixture.ts', import.meta.url))], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, origin: peer.url.origin })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30000)
    try {
      const [exit, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      const output = fileURLToPath(new URL('../../../artifacts/go-parity/sdk-analytics-environment/', import.meta.url))
      await mkdir(output, { recursive: true })
      await writeFile(join(output, 'actual-source-raw.json'), JSON.stringify({ exit, stdout, stderr, models, exports }, null, 2))
      expect({ exit, stderr }, stdout).toEqual({ exit: 0, stderr: '' })
      const result = JSON.parse(await readFile(join(home, 'sdk-analytics-source.json'), 'utf8'))
      await writeFile(join(output, 'actual-source-receipt.json'), JSON.stringify(result, null, 2))
      expect(result.caps.blocked).toEqual([])
      expect(result.sameAnalyticsModule).toBe(true)
      expect(result.contentUnchanged).toBe(true)
      expect(result.finalEnvironment).toBe(result.nextRequested)
      expect(result.settingsResponse.subtype).toBe('success')
      expect(result.settingsResponse.response.effective.future).toEqual({ directory: 'NEXT' })
      expect(models).toHaveLength(2)
      expect(JSON.stringify(models[1])).toContain('ACTUAL_SDK_ANALYTICS_READ')
      const fileEvents = exports.flatMap(row => row.body.events).filter(row => row.event_data?.event_name === 'tengu_file_operation')
      expect(fileEvents).toHaveLength(1)
      expect(result.calls).toContain('tengu_file_operation')
      expect(fileEvents[0].event_data.model).toBe('claude-sonnet-4-5-20250929')
      expect(fileEvents[0].event_data.betas).toBe('claude-code-20250219')
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill(); if (child) await child.exited
    peer.stop(true); await rm(home, { recursive: true, force: true })
  }
}, 35000)
