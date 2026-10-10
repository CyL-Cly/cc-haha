import { assertDisposableFixtureHome } from './fixture-guard'
import type { SettingsStateCase } from './cli-settings-state-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; cases: SettingsStateCase[]; values: Record<string, string>; records: Record<string, { settings: string | null; errors: string[] }>; sourceKeys: string[]; pathKeys: string[] }
assertDisposableFixtureHome(input.home, 'CLI settings state')
if (process.env.CC_HAHA_SETTINGS_STATE_FIXTURE !== '1' || Object.keys(input).some(key => !['home', 'cases', 'values', 'records', 'sourceKeys', 'pathKeys'].includes(key))) throw new Error('CLI settings state requires a declared private fixture before production imports')
const cache = await import('../../../src/utils/settings/settingsCache')
const writes = await import('../../../src/utils/settings/internalWrites')
const observations = []
for (const scenario of input.cases) {
  cache.resetSettingsCache()
  cache.clearPluginSettingsBase()
  writes.clearInternalWrites()
  const values = Object.fromEntries(Object.entries(input.values).map(([name, source]) => [name, JSON.parse(source)]))
  const records = Object.fromEntries(Object.entries(input.records).map(([name, value]) => [name, { settings: value.settings ? values[value.settings] : null, errors: value.errors.map(source => JSON.parse(source)) }]))
  let now = 1000, nowCalls = 0
  Date.now = () => { nowCalls++; return now }
  const view = (value: any, registry: Record<string, any>) => value === undefined ? { kind: 'undefined' } : value === null ? { kind: 'null' } : { kind: 'object', identity: Object.entries(registry).find(([, item]) => item === value)?.[0], source: JSON.stringify(value) }
  const steps = []
  for (const operation of scenario.operations) {
    switch (operation.kind) {
      case 'time': now = operation.now!; break
      case 'setSession': cache.setSessionSettingsCache(records[operation.ref!]); break
      case 'setSource': cache.setCachedSettingsForSource(operation.key! as any, operation.ref === null ? null : values[operation.ref!]); break
      case 'setParsed': cache.setCachedParsedFile(operation.key!, records[operation.ref!]); break
      case 'setPlugin': cache.setPluginSettingsBase(operation.ref ? values[operation.ref] : undefined); break
      case 'clearPlugin': cache.clearPluginSettingsBase(); break
      case 'reset': cache.resetSettingsCache(); break
      case 'mutateValue': values[operation.ref!][operation.key!] = JSON.parse(operation.value!); break
      case 'mutateErrors': records[operation.ref!].errors.push(JSON.parse(operation.value!)); break
      case 'mark': writes.markInternalWrite(operation.key!); break
      case 'consume': steps.push({ kind: 'consume', key: operation.key, result: writes.consumeInternalWrite(operation.key!, operation.window!) }); break
      case 'clearWrites': writes.clearInternalWrites(); break
      case 'snapshot': steps.push({
        kind: 'snapshot', session: view(cache.getSessionSettingsCache(), records),
        source: input.sourceKeys.map(key => ({ key, value: view(cache.getCachedSettingsForSource(key as any), values) })),
        parsed: input.pathKeys.map(key => ({ key, value: view(cache.getCachedParsedFile(key), records) })),
        plugin: view(cache.getPluginSettingsBase(), values),
      }); break
      default: throw new Error('unknown CLI settings state operation')
    }
  }
  observations.push({ name: scenario.name, steps, nowCalls })
}
process.stdout.write(JSON.stringify({ observations }))
