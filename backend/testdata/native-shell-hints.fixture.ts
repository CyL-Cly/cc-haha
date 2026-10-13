import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Shell hints')
if (process.env.CC_HAHA_SHELL_HINT_FIXTURE !== '1') throw new Error('Unowned shell hints fixture')
if (input.menuResponses) {
  const React = await import('react')
  let timer: any
  let cleanup: (() => void) | undefined
  const originalSetTimeout = globalThis.setTimeout
  const originalClearTimeout = globalThis.clearTimeout
  globalThis.setTimeout = ((callback: any, ms: number, ...args: any[]) => { timer = { callback, ms, args }; return 1 }) as any
  globalThis.clearTimeout = (() => {}) as any
  mock.module('react', () => ({ ...React, useRef: (value: any) => ({ current: value }), useEffect: (effect: any) => { cleanup = effect() } }))
  const Box = () => null, Text = () => null, Select = () => null, PermissionDialog = () => null
  mock.module(new URL('../../src/ink.tsx', import.meta.url).pathname, () => ({ Box, Text }))
  mock.module(new URL('../../src/components/CustomSelect/select.tsx', import.meta.url).pathname, () => ({ Select }))
  mock.module(new URL('../../src/components/permissions/PermissionDialog.tsx', import.meta.url).pathname, () => ({ PermissionDialog }))
  const { PluginHintMenu } = await import('../../src/components/ClaudeCodeHint/PluginHintMenu')
  const findSelect = (node: any): any => {
    if (!node) return null
    if (Array.isArray(node)) return node.map(findSelect).find(Boolean)
    if (node.type === Select) return node
    return findSelect(node.props?.children)
  }
  const text = (node: any): string => typeof node === 'string' ? node : Array.isArray(node) ? node.map(text).join('') : text(node?.props?.children ?? '')
  const rows = []
  for (const choice of input.menuResponses) {
    const responses: string[] = []
    const element = PluginHintMenu({ pluginName: 'owned', marketplaceName: 'agent-skills', pluginDescription: 'Actual description', sourceCommand: 'vendor', onResponse: response => responses.push(response) }) as any
    const select = findSelect(element)
    if (!select) throw new Error('Actual Source Select unavailable')
    if (choice === 'timeout') timer.callback(...timer.args)
    else if (choice === 'cancel') select.props.onCancel()
    else select.props.onChange(choice)
    rows.push({ title: element.props.title, options: select.props.options.map((option: any) => ({ label: text(option.label), value: option.value })), responses, timeoutMs: timer.ms })
    cleanup?.()
  }
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  process.stdout.write(JSON.stringify({ menuRows: rows }) + '\n')
  process.exit(0)
}
if (input.realGate || input.stripRows) {
  // Run the actual frozen function bodies with only their primitive settings
  // and feature state dependencies. No copied gate/parser implementation.
  const { parse } = await import('acorn')
  const declarations = async (file: string, names: string[]) => {
    const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(file, import.meta.url)).text())
    const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' }) as any
    const found = new Map<string, string>()
    for (const top of ast.body) {
      const node = top.type === 'ExportNamedDeclaration' ? top.declaration : top
      if (node?.type === 'FunctionDeclaration' && names.includes(node.id.name)) found.set(node.id.name, source.slice(node.start, node.end))
    }
    if (found.size !== names.length) throw new Error('Actual Source function unavailable: ' + file)
    return names.map(name => found.get(name)).join('\n')
  }
  if (input.stripRows) {
    const fn = new Function(`${await declarations('../../src/tools/BashTool/utils.ts', ['stripEmptyLines'])}; return stripEmptyLines`)()
    process.stdout.write(JSON.stringify({ strippedRows: input.stripRows.map((row: string) => fn(row)) }) + '\n')
  } else {
    const env = input.environment ?? {}
    const keys = await declarations('../../src/constants/keys.ts', ['getGrowthBookClientKey'])
    const key = new Function('process', 'isEnvTruthy', `${keys};return getGrowthBookClientKey`)({ env }, () => false)
    const bodies = await declarations('../../src/services/analytics/growthbook.ts', ['getEnvOverrides', 'getConfigOverrides', 'isGrowthBookEnabled', 'getFeatureValue_CACHED_MAY_BE_STALE'])
    const fn = new Function('process', 'getGlobalConfig', 'getGrowthBookClientKey', 'logForDebugging', 'logError', 'is1PEventLoggingEnabled', `let envOverrides=null;let envOverridesParsed=false;const experimentDataByFeature=new Map();const pendingExposures=new Set();const remoteEvalFeatureValues=new Map();function logExposureForFeature(){}\n${bodies};return getFeatureValue_CACHED_MAY_BE_STALE`)
    const getter = fn({ env }, () => input.global ?? {}, key, () => {}, () => {}, () => true)
    process.stdout.write(JSON.stringify({ resolvedGate: getter('tengu_lapis_finch', false) }) + '\n')
  }
  process.exit(0)
}
let config = input.global ?? {}
let gate = input.enabled ?? false
let installed: string[] = []
let blocked: string[] = []
for (const key of ['USER_TYPE', 'CLAUDE_INTERNAL_FC_OVERRIDES']) {
  if (input.environment?.[key] === undefined) delete process.env[key]
  else process.env[key] = input.environment[key]
}
const modulePath = (name: string) => new URL('../src/' + name, import.meta.url).pathname.replace('/backend/src/', '/src/')
mock.module(modulePath('utils/debug.ts'), () => ({ logForDebugging() {} }))
if (!input.realGate) mock.module(modulePath('services/analytics/growthbook.ts'), () => ({ getFeatureValue_CACHED_MAY_BE_STALE: () => gate }))
mock.module(modulePath('services/analytics/index.ts'), () => ({ logEvent() {} }))
mock.module(modulePath('utils/config.ts'), () => ({ getGlobalConfig: () => config, saveGlobalConfig: (update: any) => { config = update(config) } }))
mock.module(modulePath('utils/plugins/installedPluginsManager.ts'), () => ({ isPluginInstalled: (id: string) => installed.includes(id) }))
mock.module(modulePath('utils/plugins/pluginPolicy.ts'), () => ({ isPluginBlockedByPolicy: (id: string) => blocked.includes(id) }))
mock.module(modulePath('utils/plugins/marketplaceManager.ts'), () => ({ getPluginById: async (id: string) => input.catalog?.[id] ?? null }))
const parser = await import('../../src/utils/claudeCodeHints')
const recommendation = await import('../../src/utils/plugins/hintRecommendation')
const resolvedGate = input.realGate ? (await import('../../src/services/analytics/growthbook')).getFeatureValue_CACHED_MAY_BE_STALE('tengu_lapis_finch', false) : undefined
const rows = (input.rows ?? []).map((row: any) => parser.extractClaudeCodeHints(row.output, row.command))
const states: any[] = []
for (const action of input.actions ?? []) {
  if (action.kind === 'record') {
    gate = action.enabled ?? gate
    installed = action.installed ?? []
    blocked = action.blocked ?? []
    recommendation.maybeRecordPluginHint(action.hint)
  } else if (action.kind === 'clear') parser.clearPendingHint()
  else if (action.kind === 'shown') parser.markShownThisSession()
  else if (action.kind === 'response') {
    recommendation.markHintPluginShown(action.id)
    if (action.disable) recommendation.disableHintRecommendations()
  } else if (action.kind === 'resolve') {
    const hint = parser.getPendingHintSnapshot()
    states.push({ resolved: hint ? await recommendation.resolvePluginHint(hint) : null })
    continue
  }
  states.push({ pending: parser.getPendingHintSnapshot(), shown: parser.hasShownHintThisSession(), config })
}
process.stdout.write(JSON.stringify({ rows, states, resolvedGate }) + '\n')
