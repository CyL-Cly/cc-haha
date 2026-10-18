import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { requestContextWire } from './request-context-fixtures'

function strings(value: unknown): string[] {
  if (typeof value === 'string') return [value]
  if (Array.isArray(value)) return value.flatMap(strings)
  if (value !== null && typeof value === 'object') return Object.values(value).flatMap(strings)
  return []
}

test('actual SDK default parallel prompt and user context preserve captured paths across environment updates and warm turns', async () => {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'sdk-user-instructions-concurrent-')))
  const requests: unknown[] = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    expect(request.method).toBe('POST')
    expect(new URL(request.url).pathname).toBe('/v1/messages')
    requests.push(await request.json())
    return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'actual-sdk-instructions' } })
  } })
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'))
    await mkdir(join(home, 'bin'))
    const env = createSandboxedTestEnvironment(home, {
      CC_HAHA_USER_INSTRUCTIONS_SDK_ENVIRONMENT: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), BUN_OPTIONS: '--no-env-file',
      NODE_ENV: 'production', USER_TYPE: 'external', TZ: 'UTC', ANTHROPIC_API_KEY: 'offline-fixture-key', ANTHROPIC_BASE_URL: server.url.origin,
      DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_MAX_RETRIES: '0',
      CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', USE_BUILTIN_RIPGREP: 'false', PATH: join(home, 'bin'),
    })
    expect(env.CLAUDE_CODE_SIMPLE).toBeUndefined()
    expect(env.CLAUDE_CODE_DISABLE_AUTO_MEMORY).toBeUndefined()
    expect(env.CLAUDE_CODE_DISABLE_CLAUDE_MDS).toBeUndefined()
    child = Bun.spawn(['/usr/bin/sandbox-exec', '-f', fileURLToPath(new URL('../../../backend/testdata/mcp-loopback-only.sb', import.meta.url)), process.env.CC_HAHA_SOURCE_BUN ?? process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', fileURLToPath(new URL('./native-user-instructions-sdk-environment.fixture.ts', import.meta.url))], {
      cwd: home, env, stdin: new Blob([JSON.stringify({ home, origin: server.url.origin, verifySettings: true })]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr || stdout).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(await readFile(join(home, 'sdk-user-instructions-environment-result.json'), 'utf8'))
      const artifacts = fileURLToPath(new URL('../../../artifacts/go-parity/sdk-user-instructions-concurrent/', import.meta.url))
      await mkdir(artifacts, { recursive: true })
      await writeFile(join(artifacts, 'source-raw.json'), JSON.stringify({ result, requests, stdout, stderr, code }, null, 2))
      expect(result.code).toBe(0)
      expect(result.caps.blocked).toEqual([])
      // All credential-store queries use the disposable fixture's empty OS
      // primitive, and remain visible instead of reading a machine Keychain.
      expect(result.caps.secureReads).toEqual(['read', 'read', 'read'])
      expect(result.heldRead).toBe(true)
      expect(result.heldMkdir).toBe(true)
      expect(result.results).toBe(2)
      expect(result.protectedFilesUnchanged).toBe(true)
      expect(result.autoDirectoryExists).toBe(true)
      expect(result.finalEnvironment).toBe(result.nextRequested)
      expect(result.settingsResponse.subtype).toBe('success')
      expect(result.settingsResponse.request_id).toBe('sdk-directory-settings')
      expect(result.settingsResponse.response.effective.future).toEqual({ owned: true, directory: 'NEXT' })
      expect(result.settingsResponse.response.sources).toEqual([{ source: 'userSettings', settings: { future: { owned: true, directory: 'NEXT' } } }])
      expect(result.settingsResponse.response.applied).toEqual({ model: 'claude-sonnet-4-5-20250929', effort: null })
      const phases = result.events.map((event: { phase: string }) => event.phase)
      // Both native completions precede the actual SDK update. Their readiness
      // order belongs to the OS; no sorting or chosen completion order is fed
      // back to the original caller.
      expect(phases.slice(0, 2)).toHaveLength(2)
      expect(phases.slice(0, 2)).toContain('User native read completed and held')
      expect(phases.slice(0, 2)).toContain('Memory native mkdir completed and held')
      expect(phases.slice(2)).toEqual([
        'both original concurrent producer completions held', 'actual SDK environment message consumed', 'actual SDK settings response',
        'Memory completion released', 'User completion released', 'actual result', 'actual result',
      ])
      expect(result.mkdirs).toEqual([result.autoDirectory])
      expect(result.reads.filter((path: string) => path === join(result.initial, 'CLAUDE.md'))).toHaveLength(1)
      expect(result.reads).not.toContain(join(result.nextPhysical, 'CLAUDE.md'))
      expect(result.reads).toContain(join(result.nextPhysical, 'rules', 'ordinary.md'))
      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const content = strings(request).join('\n')
        expect(content.match(/ACTUAL_SDK_USER_INITIAL/g)).toHaveLength(1)
        expect(content.match(/ACTUAL_SDK_RULE_NEXT/g)).toHaveLength(1)
        expect(content).not.toContain('ACTUAL_SDK_USER_NEXT')
        expect(content).not.toContain('ACTUAL_SDK_RULE_INITIAL')
        expect(content).toContain(result.autoDirectory)
        expect(content).toContain(`Contents of ${join(result.initial, 'CLAUDE.md')}`)
        expect(content).toContain(`Contents of ${join(result.nextPhysical, 'rules', 'ordinary.md')}`)
      }
      expect(strings(requests[0]).join('\n')).toContain('SDK_INSTRUCTIONS_FIRST_HUMAN')
      expect(strings(requests[0]).join('\n')).not.toContain('SDK_INSTRUCTIONS_SECOND_HUMAN')
      expect(strings(requests[1]).join('\n')).toContain('SDK_INSTRUCTIONS_SECOND_HUMAN')
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill()
    if (child) await child.exited
    server.stop(true)
    await rm(home, { recursive: true, force: true })
  }
}, 35000)
