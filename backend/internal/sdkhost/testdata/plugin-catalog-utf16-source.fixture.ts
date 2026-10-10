import { join, dirname } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {
  absentManagedSettingsFixturePaths,
  installClientFixtureCapabilities,
} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual lone UTF16 catalog')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LONE_CATALOG_PROBE !== '1') {
  throw new Error('Owned pinned fixture required')
}
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: [
    ...absentManagedSettingsFixturePaths(),
    '/Library/Application Support/ClaudeCode/.claude/skills',
    '/Library/Application Support/ClaudeCode/.claude/agents',
    '/Library/Application Support/ClaudeCode/.claude/output-styles',
    '/usr/bin/rg',
    '/bin/rg',
    join(dirname(process.execPath), 'rg'),
    '/home/claude/.claude/remote/.api_key',
    '/home/claude/.claude/remote/.oauth_token',
  ],
})
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({
  getLegacyApiKeyPrefetchResult: () => ({ stdout: null }),
  clearLegacyApiKeyPrefetch() {},
  startKeychainPrefetch() { throw new Error('No keychain') },
  ensureKeychainPrefetchCompleted: async () => {},
}))
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config')
config.enableConfigs()
const { createPluginFromPath } = await import('../../../../src/utils/plugins/pluginLoader')
const { loadPluginCommandsFromEnabledPlugins, loadPluginSkillsFromEnabledPlugins } =
  await import('../../../../src/utils/plugins/loadPluginCommands')
const { formatCommandsWithinBudget } = await import('../../../../src/tools/SkillTool/prompt')
const { formatDescriptionWithSource, getCommandName } = await import('../../../../src/commands')
const units = (text: string) => Array.from({ length: text.length }, (_, i) => text.charCodeAt(i))
const summarize = (rows: any[]) => rows.map(row => ({
  name: units(row.name),
  description: units(row.description),
  argumentHint: units(row.argumentHint ?? ''),
}))
const consume = (value: any) => ({
  control: summarize(JSON.parse(value.control)),
  system: summarize(JSON.parse(value.system)),
  guide: JSON.parse(value.guide).map((row: any) => ({
    type: row.type, source: row.source, name: units(row.name), description: units(row.description),
  })),
  metadata: JSON.parse(value.metadata).map((row: any) => ({
    name: units(row.name), description: units(row.description),
    userFacingName: units(row.userFacingName), disableModelInvocation: row.disableModelInvocation,
  })),
  skillListing: units(JSON.parse(JSON.stringify(value.skillListing))),
})
const rows = []
for (let i = 0; i < input.directories.length; i++) {
  const created = await createPluginFromPath(input.directories[i], 'owned@inline', true, 'fallback', true)
  const commands = [
    ...await loadPluginCommandsFromEnabledPlugins([created.plugin]),
    ...await loadPluginSkillsFromEnabledPlugins([created.plugin]),
  ]
  const catalog = commands.filter(command => command.userInvocable !== false).map(command => ({
    name: getCommandName(command), description: formatDescriptionWithSource(command),
    argumentHint: command.argumentHint || '',
  }))
  const source = {
    control: JSON.stringify(catalog),
    system: JSON.stringify(commands.filter(command => command.userInvocable !== false).map(command => ({
      name: command.name, description: command.description,
      ...(command.argumentHint ? { argumentHint: command.argumentHint } : {}),
    }))),
    guide: JSON.stringify(commands.map(command => ({
      type: command.type, name: command.name, description: command.description, source: command.source,
    }))),
    metadata: JSON.stringify(commands.map(command => ({
      name: command.name, description: command.description, userFacingName: getCommandName(command),
      disableModelInvocation: command.disableModelInvocation,
    }))),
    skillListing: formatCommandsWithinBudget(commands),
  }
  const go = input.goJSON[i]
  rows.push({
    manifest: created.plugin.manifest, loaderErrors: created.errors,
    sourceCatalogJSON: source, sourceConsumerUnits: consume(source),
    goCatalogJSON: go, goConsumerUnits: consume(go),
    rawSourceDescriptions: commands.map(command => units(command.description)),
  })
}
if (capabilities.blocked.length) throw new Error('Capability denied:' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, capabilities: { blocked: capabilities.blocked } }) + '\n')
