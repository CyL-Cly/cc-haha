import { expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

test('whole original ordinary command sinks use actual auth/trust producers for 1P', async () => {
  const root = fileURLToPath(new URL('../../../', import.meta.url))
  const sources = [
    'src/main.tsx', 'src/entrypoints/init.ts', 'src/bootstrap/state.ts',
    'src/utils/sinks.ts', 'src/utils/config.ts', 'src/utils/auth.ts', 'src/utils/http.ts',
    'src/utils/settings/settings.ts', 'src/utils/model/providers.ts', 'src/utils/model/model.ts',
    'src/services/analytics/index.ts', 'src/services/analytics/sink.ts', 'src/services/analytics/metadata.ts',
    'src/services/analytics/firstPartyEventLogger.ts', 'src/services/analytics/firstPartyEventLoggingExporter.ts',
    'src/migrations/migrateAutoUpdatesToSettings.ts', 'src/migrations/migrateBypassPermissionsAcceptedToSettings.ts',
    'src/migrations/migrateEnableAllProjectMcpServersToSettings.ts', 'src/migrations/resetProToOpusDefault.ts',
    'src/migrations/migrateSonnet1mToSonnet45.ts', 'src/migrations/migrateLegacyOpusToCurrent.ts',
    'src/migrations/migrateSonnet45ToSonnet46.ts', 'src/migrations/migrateOpusToOpus1m.ts',
    'src/migrations/migrateReplBridgeEnabledToRemoteControlAtStartup.ts', 'src/migrations/resetAutoModeOptInForDefaultOffer.ts',
  ]
  const sha = (path: string) => createHash('sha256').update(readFileSync(join(root, path))).digest('hex')
  const before = Object.fromEntries(sources.map(path => [path, sha(path)]))
  const owned = mkdtempSync(join(tmpdir(), 'whole-command-sinks-auth-'))
  const requests: Array<Record<string, unknown>> = []
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    if (request.method === 'HEAD' && new URL(request.url).pathname === '/safe') return new Response(null, { status: 204 })
    if (request.method !== 'POST' || new URL(request.url).pathname !== '/events') throw new Error('Unowned actual Source HTTP request')
    requests.push({ headers: Object.fromEntries(request.headers), body: await request.json() })
    return Response.json({ accepted: true })
  } })
  const profiles = (process.env.CC_HAHA_COMMAND_SINKS_AUTH_PROFILES ?? 'ordinary-env,ordinary-helper-cold,ordinary-subscriber,ordinary-subscriber-no-profile,ordinary-subscriber-expired,ordinary-pro-default,ordinary-pro-custom').split(',')
  try {
    for (const kind of profiles) {
      const home = join(owned, kind)
      mkdirSync(home, { recursive: true })
      const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: owned, CC_HAHA_COMMAND_SINKS_AUTH_SOURCE: '1', NODE_ENV: 'production', USER_TYPE: 'external', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', ANTHROPIC_BASE_URL: peer.url.origin + '/launch', NO_COLOR: '1' }, { PATH: '/usr/bin:/bin' })
      const beforeRequests = requests.length
      const process = Bun.spawn([globalThis.process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/native-command-sinks-auth.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, kind, origin: peer.url.origin })]), stdout: 'pipe', stderr: 'pipe' })
      const [exit, stdout, stderr] = await Promise.all([process.exited, new Response(process.stdout).text(), new Response(process.stderr).text()])
      expect({ exit, stdout, stderr }).toEqual({ exit: 0, stdout: '[]\n', stderr: '' })
      const receipt = JSON.parse(readFileSync(join(home, 'command-sinks-auth-source.json'), 'utf8'))
      if (receipt.caps.blocked.length) console.log(JSON.stringify({ kind, blockedDiagnostic: receipt }))
      expect(receipt.caps.blocked).toEqual([])
      expect(receipt.deniedRequests).toEqual([])
      expect(receipt.exits).toEqual([0])
      expect(receipt.userSettingsUnchanged).toBe(true)
      expect(receipt.nonInteractive).toBe(true)
      expect(receipt.trust).toBe(false)
      expect(receipt.initializations).toEqual([{ phase: 'actual-init-1p', nonInteractive: true, trust: false }])
      expect(receipt.helperExecuted).toBe(false)
      expect(receipt.helperCached).toBeNull()
      expect(receipt.subscriber).toBe(kind.startsWith('ordinary-subscriber') || kind.startsWith('ordinary-pro-'))
      const actual = requests.slice(beforeRequests)
      expect(actual).toHaveLength(1)
      expect(receipt.prepares).toHaveLength(1)
      const headers = actual[0]!.headers as Record<string, string>
      if (kind === 'ordinary-env') {
        expect(headers['x-api-key']).toBe('fake-owned-command-1p-env-key')
        expect(headers.authorization).toBeUndefined()
      } else if (kind === 'ordinary-subscriber' || kind.startsWith('ordinary-pro-')) {
        expect(headers.authorization).toBe('Bearer fake-owned-command-cached-token')
        expect(headers['anthropic-beta']).toBe('oauth-2025-04-20')
        expect(headers['x-api-key']).toBeUndefined()
      } else {
        expect(headers.authorization).toBeUndefined()
        expect(headers['x-api-key']).toBeUndefined()
      }
      const body = actual[0]!.body as { events: Array<{ event_data: Record<string, unknown> }> }
      // The real preAction runs this fresh-global migration after initSinks,
      // before the actual plugin handler. Keep that genuine producer visible.
      expect(body.events.map(row => row.event_data.event_name)).toEqual(['tengu_reset_pro_to_opus_default', 'tengu_plugin_list_command'])
      const migration = body.events[0]!.event_data
      const migrationMetadata = JSON.parse(Buffer.from(migration.additional_metadata as string, 'base64').toString('utf8'))
      expect(migrationMetadata).toEqual(kind.startsWith('ordinary-pro-') ? { skipped: false, had_custom_model: kind === 'ordinary-pro-custom' } : { skipped: true })
      expect(receipt.globalAfter.opusProMigrationComplete).toBe(true)
      expect(receipt.globalAfter.migrationVersion).toBe(11)
      expect(receipt.globalAfter.futureGlobal).toEqual({ keep: true })
      if (kind === 'ordinary-pro-default') expect(receipt.globalAfter.opusProMigrationTimestamp).toBeNumber()
      else expect(receipt.globalAfter.opusProMigrationTimestamp).toBeUndefined()
      console.log(JSON.stringify({ kind, receipt, physicalHTTP: actual, sourceSHA: before }))
    }
    expect(Object.fromEntries(sources.map(path => [path, sha(path)]))).toEqual(before)
  } finally {
    peer.stop(true)
    rmSync(owned, { recursive: true, force: true })
  }
}, 30000)
