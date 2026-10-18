import { expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

test('whole original ordinary preAction consumes v11 model, policy, Git MCP and detached changelog migrations', async () => {
  const root = fileURLToPath(new URL('../../../', import.meta.url))
  const sources = [
    'src/main.tsx', 'src/utils/config.ts', 'src/utils/auth.ts', 'src/utils/releaseNotes.ts',
    'src/utils/settings/settings.ts', 'src/utils/settings/mdm/settings.ts', 'src/utils/git/gitignore.ts',
    'src/utils/model/model.ts', 'src/utils/model/providers.ts',
    'src/migrations/migrateAutoUpdatesToSettings.ts', 'src/migrations/migrateBypassPermissionsAcceptedToSettings.ts',
    'src/migrations/migrateEnableAllProjectMcpServersToSettings.ts', 'src/migrations/resetProToOpusDefault.ts',
    'src/migrations/migrateSonnet1mToSonnet45.ts', 'src/migrations/migrateLegacyOpusToCurrent.ts',
    'src/migrations/migrateSonnet45ToSonnet46.ts', 'src/migrations/migrateOpusToOpus1m.ts',
    'src/migrations/migrateReplBridgeEnabledToRemoteControlAtStartup.ts', 'src/migrations/resetAutoModeOptInForDefaultOffer.ts',
  ]
  const sha = (path: string) => createHash('sha256').update(readFileSync(join(root, path))).digest('hex')
  const before = Object.fromEntries(sources.map(path => [path, sha(path)]))
  const owned = mkdtempSync(join(tmpdir(), 'whole-command-migrations-'))
  const requests: Array<Record<string, unknown>> = []
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    if (request.method === 'HEAD' && new URL(request.url).pathname === '/safe') return new Response(null, { status: 204 })
    if (request.method !== 'POST' || new URL(request.url).pathname !== '/events') throw new Error('Unowned actual Source HTTP request')
    requests.push({ headers: Object.fromEntries(request.headers), body: await request.json() })
    return Response.json({ accepted: true })
  } })
  const profiles = (process.env.CC_HAHA_COMMAND_MIGRATIONS_PROFILES ?? 'sonnet-pro,sonnet-max-from-1m,opus-max,opus-pro,opus-unknown,bypass-managed,bypass-project-only,mcp-git,mcp-git-privacy,changelog-new,changelog-existing,changelog-write-failure,already-v11').split(',')
  try {
    for (const kind of profiles) {
      const home = join(owned, kind)
      mkdirSync(home, { recursive: true })
      const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: owned, CC_HAHA_COMMAND_MIGRATIONS_SOURCE: '1', NODE_ENV: 'production', USER_TYPE: 'external', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', ANTHROPIC_BASE_URL: peer.url.origin + '/launch', NO_COLOR: '1' }, { PATH: '/usr/bin:/bin' })
      const beforeRequests = requests.length
      const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/native-command-migrations.fixture.ts')], { cwd: home, env, stdin: new Blob([JSON.stringify({ home, kind, origin: peer.url.origin })]), stdout: 'pipe', stderr: 'pipe' })
      const [exit, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect({ exit, stdout, stderr }).toEqual({ exit: 0, stdout: '[]\n', stderr: '' })
      const receipt = JSON.parse(readFileSync(join(home, 'command-migrations-source.json'), 'utf8'))
      console.log(JSON.stringify({ kind, receipt, physicalHTTP: requests.slice(beforeRequests), sourceSHA: before }))
      expect(receipt.moduleIdentity).toEqual({ analytics: true, config: true, analyticsLogEvent: true, configGetter: true })
      expect(receipt.caps.blocked).toEqual([])
      expect(receipt.deniedRequests).toEqual([])
      expect(receipt.exits).toEqual([0])
      expect(receipt.globalAfter.migrationVersion).toBe(11)
      expect(receipt.globalAfter.futureGlobal).toEqual({ keep: true })
      expect(receipt.userAfter.futurePrivate).toEqual({ keep: true })
      const actual = requests.slice(beforeRequests)
      expect(actual).toHaveLength(kind === 'mcp-git-privacy' ? 0 : 1)
      const names = kind === 'mcp-git-privacy'
        ? receipt.eventCalls.map((row: any) => row.name)
        : (actual[0]!.body as { events: Array<{ event_data: Record<string, unknown> }> }).events.map(row => row.event_data.event_name)
      expect(receipt.telemetryDisabled).toBe(kind === 'mcp-git-privacy')
      expect(names.at(-1)).toBe('tengu_plugin_list_command')
      if (kind === 'sonnet-pro' || kind === 'sonnet-max-from-1m') {
        expect(receipt.userAfter.model).toBe(kind === 'sonnet-pro' ? 'sonnet' : 'sonnet[1m]')
        expect(receipt.globalAfter.sonnet45To46MigrationTimestamp).toBeNumber()
        expect(names).toContain('tengu_sonnet45_to_46_migration')
        if (kind === 'sonnet-max-from-1m') expect(receipt.mutations.filter((row: any) => row.kind === 'updateSettingsForSource')).toHaveLength(2)
      } else if (kind.startsWith('opus-')) {
        expect(receipt.userAfter.model).toBe(kind === 'opus-max' ? undefined : 'opus')
        expect(names.includes('tengu_opus_to_opus1m_migration')).toBe(kind === 'opus-max')
        if (kind === 'opus-max') expect(receipt.mutations.find((row: any) => row.kind === 'updateSettingsForSource').undefinedKeys).toEqual(['model'])
      } else if (kind.startsWith('bypass-')) {
        expect(receipt.globalAfter.bypassPermissionsModeAccepted).toBeUndefined()
        expect(receipt.userAfter.skipDangerousModePermissionPrompt).toBe(kind === 'bypass-managed' ? undefined : true)
        expect(names).toContain('tengu_migrate_bypass_permissions_accepted')
        if (kind === 'bypass-managed') expect(receipt.policyAfter.skipDangerousModePermissionPrompt).toBe(true)
      } else if (kind.startsWith('mcp-git')) {
        const local = JSON.parse(receipt.localAfter.text)
        expect(local).toEqual({ enableAllProjectMcpServers: false, enabledMcpjsonServers: ['local', 'shared', 'old'], disabledMcpjsonServers: ['local-disabled', 'disabled'], futureLocal: { keep: true } })
        const project = Object.values(receipt.globalAfter.projects)[0] as Record<string, unknown>
        expect(project.enableAllProjectMcpServers).toBeUndefined()
        expect(project.enabledMcpjsonServers).toBeUndefined()
        expect(project.disabledMcpjsonServers).toBeUndefined()
        expect(project.futureProject).toEqual({ keep: true })
        expect(receipt.gitignoreAfter.text).toBe('**/.claude/settings.local.json\n')
        expect(receipt.spawns).toHaveLength(1)
        expect(names).toContain('tengu_migrate_mcp_approval_fields_success')
      } else if (kind.startsWith('changelog-')) {
        expect(receipt.globalAfter.cachedChangelog).toBeUndefined()
        expect(receipt.backgroundJobCount).toBe(1)
        if (kind === 'changelog-write-failure') expect(receipt.changelogAfter).toEqual({ directory: true })
        else expect(receipt.changelogAfter.text).toBe(kind === 'changelog-new' ? '# owned migration changelog\n' : '# already-owned cache\n')
      } else if (kind === 'already-v11') {
        expect(receipt.globalAfter.opusProMigrationComplete).toBeUndefined()
        expect(receipt.globalAfter.firstStartTime).toBeString()
        expect(receipt.mutations.every((row: any) => row.kind === 'saveGlobalConfig' && row.global.opusProMigrationComplete === undefined)).toBe(true)
        expect(names).toEqual(['tengu_plugin_list_command'])
      }
    }
    expect(Object.fromEntries(sources.map(path => [path, sha(path)]))).toEqual(before)
  } finally {
    peer.stop(true)
    rmSync(owned, { recursive: true, force: true })
  }
}, 30000)
