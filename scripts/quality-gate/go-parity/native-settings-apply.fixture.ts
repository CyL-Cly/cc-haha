import { parse } from 'acorn'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Native settings apply')
if (process.env.CC_HAHA_SETTINGS_APPLY_FIXTURE !== '1') throw new Error('Unowned settings apply fixture')
await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { applyPermissionUpdate, applyPermissionUpdates } = await import('../../../src/utils/permissions/PermissionUpdate')
const { permissionRuleValueFromString } = await import('../../../src/utils/permissions/permissionRuleParser')
const { createDisabledBypassPermissionsContext, reconcileAutoModePermissionsAfterSettingsChange } = await import('../../../src/utils/permissions/permissionSetup')
const config = await import('../../../src/utils/config')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
const { _resetForTesting } = await import('../../../src/utils/permissions/autoModeState')
mkdirSync(join(process.env.HOME!, '.claude'), {recursive:true})
config.enableConfigs()
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/permissions/permissions.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const extracts = ['convertRulesToUpdates', 'syncPermissionRulesFromDisk'].map(name => {
  const node = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!node) throw new Error('Missing actual settings apply Source ' + name)
  return source.slice(node.start, node.end)
}).join('\n')
const outputs = []
for (const row of input.rows) {
  writeFileSync(join(process.env.HOME!, '.claude/settings.json'),JSON.stringify(row.settings))
  resetSettingsCache()
  _resetForTesting()
  const sync = new Function('shouldAllowManagedPermissionRulesOnly', 'applyPermissionUpdate', 'applyPermissionUpdates', 'SETTING_SOURCES', extracts + '\nreturn syncPermissionRulesFromDisk')(() => row.policy?.allowManagedPermissionRulesOnly === true, applyPermissionUpdate, applyPermissionUpdates, ['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings'])
  const rules = []
  for (const source of ['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings']) {
    if (row.policy?.allowManagedPermissionRulesOnly && source !== 'policySettings') continue
    for (const behavior of ['allow', 'deny', 'ask']) {
      for (const raw of row.rules[source]?.[behavior] ?? []) rules.push({ source, ruleBehavior: behavior, ruleValue: permissionRuleValueFromString(raw) })
    }
  }
  let context = sync(row.context, rules)
  if (context.isBypassPermissionsModeAvailable && row.settings.permissions?.disableBypassPermissionsMode === 'disable') context = createDisabledBypassPermissionsContext(context)
  context = reconcileAutoModePermissionsAfterSettingsChange(context,rules)
  outputs.push(context)
}
process.stdout.write(JSON.stringify(outputs))
