import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { permissionRuleValueFromString, permissionRuleValueToString } from '../../../src/utils/permissions/permissionRuleParser'
import { permissionModeTitle } from '../../../src/utils/permissions/PermissionMode'
import { SETTING_SOURCES, getSettingSourceDisplayNameLowercase } from '../../../src/utils/settings/constants'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

assertDisposableFixtureHome(process.env.HOME, 'Actual permission debug display')
if (process.env.CC_HAHA_PERMISSION_DEBUG_SOURCE !== '1' || Bun.version !== '1.3.14') throw new Error('Unowned permission debug Source')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function compile(path: string, deps: Record<string, unknown>, returns: string, select?: string[]) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const statements = source.statements.filter((node: any) => !ts.isImportDeclaration(node) && (!select || ts.isFunctionDeclaration(node) && select.includes(node.name?.text)))
  const body = statements.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + returns)(...Object.values(deps))
}
const rules = compile('../../../src/utils/permissions/permissions.ts', {
  PERMISSION_RULE_SOURCES: [...SETTING_SOURCES, 'cliArg', 'command', 'session'], permissionRuleValueFromString
}, '({getAllowRules,getAskRules,getDenyRules})', ['getAllowRules', 'getAskRules', 'getDenyRules'])
const shadow = compile('../../../src/utils/permissions/shadowedRuleDetection.ts', {
  ...rules, BASH_TOOL_NAME: 'Bash', permissionRuleSourceDisplayString: getSettingSourceDisplayNameLowercase
}, '({detectUnreachableRules,isSharedSettingSource})')
const input = await Bun.stdin.json()
const results = (input.rows ?? []).map((row: any) => ({
  unreachable: shadow.detectUnreachableRules(row.context, { sandboxAutoAllowEnabled: row.sandboxAutoAllow }).map((item: any) => ({
    source: item.rule.source, behavior: item.rule.ruleBehavior, rule: item.rule.ruleValue,
    reason: item.reason, fix: item.fix, shadowType: item.shadowType
  })),
  ruleDisplays: row.ruleValues.map((rule: any) => permissionRuleValueToString(rule)),
  sourceDisplays: row.sources.map((source: any) => getSettingSourceDisplayNameLowercase(source)),
  modeDisplays: row.modes.map((mode: any) => permissionModeTitle(mode))
}))
const renders = []
const layouts: any[] = []
if (input.render?.length) {
  const absentPaths = absentManagedSettingsFixturePaths()
  const packagedRg = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
  if (!fs.existsSync(packagedRg)) absentPaths.push(packagedRg)
  fs.mkdirSync(join(process.env.HOME!, 'bin'), { recursive: true })
  process.env.PATH = join(process.env.HOME!, 'bin')
  const caps = await installClientFixtureCapabilities(process.env.HOME!, { absentPaths })
  if (input.captureLayout) {
    const { mock } = await import('bun:test')
    const dom = await import('../../../src/ink/dom')
    const originalCreateNode = dom.createNode
    mock.module('../../../src/ink/dom', () => ({ ...dom, createNode(name: any) {
      const node = originalCreateNode(name)
      if (name === 'ink-root') {
        let compute: any
        let captured = false
        Object.defineProperty(node, 'onComputeLayout', { configurable: true, get: () => compute, set(value) {
          compute = () => {
            value()
            if (captured) return
            captured = true
            const rows: any[] = []
            const walk = (entry: any, depth = 0) => {
              rows.push({ depth, name: entry.nodeName, text: entry.nodeValue, style: entry.style, flex: entry.yogaNode ? { basis: entry.yogaNode.yoga?._flexBasis, main: entry.yogaNode.yoga?._mainSize, cross: entry.yogaNode.yoga?._crossSize, basisWidth: entry.yogaNode.yoga?._fbAvailCross, basisOwnerWidth: entry.yogaNode.yoga?._fbOwnerW } : undefined, layout: entry.yogaNode ? { top: entry.yogaNode.getComputedTop(), left: entry.yogaNode.getComputedLeft(), width: entry.yogaNode.getComputedWidth(), height: entry.yogaNode.getComputedHeight() } : undefined })
              for (const child of entry.childNodes ?? []) walk(child, depth + 1)
            }
            walk(node)
            layouts.push(rows)
          }
        } })
      }
      return node
    } }))
  }
  ;(globalThis as any).MACRO = { VERSION: '999.0.0-permission-debug-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
  await import('../../../preload')
  const state = await import('../../../src/bootstrap/state')
  state.setCwdState(process.env.HOME!)
  state.setOriginalCwd(process.env.HOME!)
  state.setProjectRoot(process.env.HOME!)
  state.setAllowedSettingSources(['userSettings'])
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  const React = await import('react')
  const { c: _c } = await import('react/compiler-runtime')
  const { default: chalk } = await import('chalk')
  const { default: figures } = await import('figures')
  const { Ansi, Box, Text, color, useTheme } = await import('../../../src/ink')
  const { renderToAnsiString } = await import('../../../src/utils/staticRender')
  const extractRules = compile('../../../src/utils/permissions/PermissionUpdate.ts', {}, 'extractRules', ['extractRules'])
  chalk.level = 3
  for (const row of input.render) {
    const original = compile('../../../src/components/permissions/PermissionDecisionDebugInfo.tsx', {
      React, _c, chalk, figures, Ansi, Box, Text, color, useTheme,
      useMemo: React.useMemo,
      useAppState: (selector: any) => selector({ toolPermissionContext: row.context }),
      permissionModeTitle, extractRules, permissionRuleValueToString,
      detectUnreachableRules: shadow.detectUnreachableRules,
      SandboxManager: { isSandboxingEnabled: () => row.sandboxAutoAllow, isAutoAllowBashIfSandboxedEnabled: () => row.sandboxAutoAllow },
      getSettingSourceDisplayNameLowercase,
      feature: (name: string) => name === 'TRANSCRIPT_CLASSIFIER'
    }, 'PermissionDecisionDebugInfo')
    const result = structuredClone(row.result)
    if (result.decisionReason?.type === 'subcommandResults') result.decisionReason.reasons = new Map(result.decisionReason.entries)
    renders.push(await renderToAnsiString(React.createElement(original, { permissionResult: result, toolName: row.toolName }), row.width))
  }
  if (caps.blocked.length || caps.secureReads.length) throw new Error('Permission debug renderer escaped private fixture: ' + JSON.stringify(caps))
}
await new Promise<void>((resolve, reject) => {
  process.stdout.write(JSON.stringify(input.render ? { rows: results, renders, layouts } : results) + '\n', error => error ? reject(error) : resolve())
})
