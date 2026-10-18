import { expect, test } from 'bun:test'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

test('ordinary Source preAction attaches real sinks and drains original queued values', async () => {
  const root = fileURLToPath(new URL('../../../', import.meta.url))
  const home = mkdtempSync(join(tmpdir(), 'command-sinks-source-'))
  const identities = ['src/main.tsx', 'src/entrypoints/init.ts', 'src/utils/sinks.ts', 'src/utils/log.ts', 'src/utils/errorLogSink.ts', 'src/services/analytics/index.ts', 'src/services/analytics/sink.ts']
  const hash = (data: Buffer) => createHash('sha256').update(data).digest('hex')
  const before = identities.map(file => hash(readFileSync(join(root, file))))
  const heads: string[] = []
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
    if (request.method !== 'HEAD' || new URL(request.url).pathname !== '/safe') throw new Error('Actual preconnect escaped private HEAD primitive')
    heads.push(request.method)
    return new Response(null, { status: 204 })
  } })
  try {
    const debugFile = join(home, 'command-debug.log')
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_COMMAND_SINKS_SOURCE: '1', NODE_ENV: 'production', USER_TYPE: 'external', DEBUG: '1', CLAUDE_CODE_DEBUG_LOGS_DIR: debugFile, DISABLE_TELEMETRY: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', ANTHROPIC_API_KEY: 'fake-command-sinks-key', ANTHROPIC_BASE_URL: peer.url.origin + '/launch', NO_COLOR: '1' }, { PATH: '/usr/bin:/bin' })
    const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/native-command-sinks-preaction.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, origin: peer.url.origin })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect({ code, stdout, stderr }).toEqual({ code: 0, stdout: '[]\n', stderr: '' })
    const receipt = JSON.parse(readFileSync(join(home, 'ordinary-command-sinks-source-receipt.json'), 'utf8'))
    expect(receipt.code).toBe(0)
    expect(receipt.protectedUnchanged).toBe(true)
    expect(receipt.caps.blocked).toEqual([])
    expect(heads).toEqual(['HEAD'])
    const events = receipt.events as Array<Record<string, unknown>>
    expect(events.filter(row => row.phase === 'before:actual-main')).toEqual([{ phase: 'before:actual-main', errorMemory: true, queuedPublicPromise: true }])
    expect(events.filter(row => row.phase === 'error:delivered')).toEqual([{ phase: 'error:delivered', sameError: true, message: 'private queued command error' }])
    const phases = events.map(row => row.phase)
    expect(phases.indexOf('attach:error:begin')).toBeLessThan(phases.indexOf('error:delivered'))
    expect(phases.indexOf('error:delivered')).toBeLessThan(phases.indexOf('attach:error:end'))
    expect(phases.indexOf('attach:error:end')).toBeLessThan(phases.indexOf('attach:analytics:begin'))
    expect(phases.indexOf('attach:analytics:end')).toBeLessThan(phases.indexOf('analytics:sync'))
    expect(events.filter(row => row.phase === 'analytics:sync')).toEqual([{ phase: 'analytics:sync', name: 'private_command_sync', sameMetadata: true, value: 41 }])
    expect(events.filter(row => row.phase === 'analytics:async')).toEqual([{ phase: 'analytics:async', name: 'private_command_async', sameMetadata: true, value: 42, originalPromise: true }])
    expect(events.filter(row => row.phase === 'analytics:actual-1p-route')).toEqual([{ phase: 'analytics:actual-1p-route', name: 'private_command_sync', value: 41 }, { phase: 'analytics:actual-1p-route', name: 'private_command_async', value: 42 }])
    const logs = readFileSync(debugFile, 'utf8')
    expect(logs).toContain('Error log sink initialized')
    expect(logs).toContain('private queued command error')
    expect(identities.map(file => hash(readFileSync(join(root, file))))).toEqual(before)
    console.log(JSON.stringify({ scope: receipt.scope, events, actualPrivateDebugLog: logs, actualHEAD: heads }))
  } finally {
    peer.stop(true)
    rmSync(home, { recursive: true, force: true })
  }
}, 20000)
