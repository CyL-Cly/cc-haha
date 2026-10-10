import { spyOn } from 'bun:test'
import { join, relative } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

type Step = {
  operation: string
  patch?: Record<string, unknown>
  unset?: string[]
  root?: string
  agent?: string
  mode?: string
  models?: { main: string; fable?: string; haiku: string; sonnet: string; opus: string }
}
type Request = { homeDir: string; projectRoot?: string; shellEnv?: Record<string, string>; migrationScope: string; steps: Step[] }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Request
assertDisposableFixtureHome(input.homeDir, 'Settings')
if (input.migrationScope !== 'current-settings-fixture' || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('Settings fixture requires disposable HOME/config and current-settings corpus')
}
const { SettingsService } = await import('../../../src/server/services/settingsService')
const { ManagedSettingsService } = await import('../../../src/server/services/managedSettingsService')
const migrations = await import('../../../src/server/services/persistentStorageMigrations')
const terminalEnvironment = await import('../../../src/utils/terminalShellEnvironment')
const settingsCache = await import('../../../src/utils/settings/settingsCache')
const gitignore = await import('../../../src/utils/git/gitignore')
const { ApiError } = await import('../../../src/server/middleware/errorHandler')
const warnings: string[] = []
console.warn = (...args: unknown[]) => { warnings.push(args.map(String).join(' ')) }
spyOn(terminalEnvironment, 'getProcessEnvWithTerminalShellEnvironment').mockImplementation(async () => input.shellEnv ?? {})
const migrationPaths: string[] = []
const originalMigration = migrations.ensurePersistentStorageUpgraded
spyOn(migrations, 'ensurePersistentStorageUpgraded').mockImplementation(async () => {
  migrationPaths.push(relative(input.homeDir, process.env.CLAUDE_CONFIG_DIR!))
  return originalMigration()
})
let invalidations = 0
const originalInvalidation = settingsCache.resetSettingsCache
spyOn(settingsCache, 'resetSettingsCache').mockImplementation(() => { invalidations++; originalInvalidation() })
const pendingGitignore: Promise<void>[] = []
const originalGitignore = gitignore.addFileGlobRuleToGitignore
spyOn(gitignore, 'addFileGlobRuleToGitignore').mockImplementation((...args) => {
  const pending = originalGitignore(...args)
  pendingGitignore.push(pending)
  return pending
})
const service = new SettingsService(input.projectRoot)
const managed = new ManagedSettingsService()
const results: unknown[] = []
for (const action of input.steps) {
  const patch = action.patch ?? {}
  for (const field of action.unset ?? []) patch[field] = undefined
  try {
    let value: unknown = null
    switch (action.operation) {
      case 'get-user': value = await service.getUserSettings(); break
      case 'get-project': value = await service.getProjectSettings(action.root); break
      case 'get-local': value = await service.getLocalSettings(action.root); break
      case 'get-merged': value = await service.getSettings(action.root); break
      case 'get-teams': value = await service.getAgentTeamsEnabled(); break
      case 'get-permission': value = await service.getPermissionMode(); break
      case 'update-user': await service.updateUserSettings(patch); break
      case 'update-project': await service.updateProjectSettings(patch, action.root); break
      case 'update-local': await service.updateLocalSettings(patch, action.root); await Promise.all(pendingGitignore); break
      case 'official-mapping': await service.updateOfficialModelMapping(action.models!); break
      case 'override': await service.updateBuiltInAgentOverride(action.agent!, patch as Parameters<InstanceType<typeof SettingsService>['updateBuiltInAgentOverride']>[1]); break
      case 'reset-override': await service.updateBuiltInAgentOverride(action.agent!, null); break
      case 'set-permission': await service.setPermissionMode(action.mode!); break
      case 'managed-read': value = await managed.readSettings(); break
      case 'managed-update': value = await managed.updateSettings(current => ({ settings: Object.assign({}, current, patch), result: 'fixture updater result' })); break
      default: throw new Error(`Unsupported settings fixture operation: ${action.operation}`)
    }
    results.push({ value })
  } catch (error) {
    results.push({ error: {
      status: error instanceof ApiError ? error.statusCode : 500,
      code: error instanceof ApiError ? error.code : 'INTERNAL_ERROR',
      message: error instanceof Error ? error.message : String(error),
    } })
  }
}
await Promise.all(pendingGitignore)
console.log(JSON.stringify({ results, warningCount: warnings.length, migrationPaths, invalidations }))
