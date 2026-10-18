import fs from 'node:fs'
import { createRequire } from 'node:module'
import React from 'react'
import figures from 'figures'
import { relative } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Original export notices')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_EXPORT_NOTICES_FIXTURE !== '1') throw new Error('Pinned Source guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-export-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { AppStateProvider } = await import('../../../src/state/AppState')
const { getLargeMemoryFiles, MAX_MEMORY_CHARACTER_COUNT } = await import('../../../src/utils/claudemd')
const { getAgentDescriptionsTotalTokens, AGENT_DESCRIPTIONS_THRESHOLD } = await import('../../../src/utils/statusNoticeHelpers')
const { formatNumber } = await import('../../../src/utils/format')
const { renderToAnsiString } = await import('../../../src/utils/staticRender')
const { Box, Text } = await import('../../../src/ink')
const filename = new URL('../../../src/utils/statusNoticeDefinitions.tsx', import.meta.url).pathname
const source = ts.createSourceFile(filename, fs.readFileSync(filename, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const names = ['largeMemoryFilesNotice', 'largeAgentDescriptionsNotice', 'claudeAiSubscriberExternalTokenNotice', 'apiKeyConflictNotice', 'bothAuthMethodsNotice', 'jetbrainsPluginNotice', 'statusNoticeDefinitions']
const nodes = source.statements.filter((node: any) => ts.isVariableStatement(node) && node.declarationList.declarations.some((declaration: any) => names.includes(declaration.name.getText(source))) || ts.isFunctionDeclaration(node) && node.name?.text === 'getActiveNotices')
if (nodes.length !== 8) throw new Error('Original six notice identities changed')
const body = ts.transpileModule(nodes.map((node: any) => node.getText(source)).join('\n'), { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replaceAll('export ', '')
const results = []
const layouts: any[] = []
function Probe({ children }: { children: React.ReactNode }) {
  const ref = React.useRef<any>(null)
  React.useLayoutEffect(() => {
    const inspect = (node: any): any => ({ type: node.nodeName, text: node.nodeValue, layout: node.yogaNode ? { left: node.yogaNode.getComputedLeft(), top: node.yogaNode.getComputedTop(), width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight() } : undefined, children: node.childNodes?.map(inspect) })
    const timer = setTimeout(() => { if (ref.current) layouts.push(inspect(ref.current)) }, 0)
    return () => clearTimeout(timer)
  }, [])
  return React.createElement(Box, { ref, flexDirection: 'column', paddingLeft: 1 }, children)
}
for (const row of input.rows) {
  // These are the external cached credential and IDE primitive observations.
  // The original predicates/render callbacks above remain byte-identical;
  // physical native producer and actual fresh export are checked separately.
  const active = new Function('React', 'Box', 'Text', 'getLargeMemoryFiles', 'MAX_MEMORY_CHARACTER_COUNT', 'figures', 'getCwd', 'relative', 'formatNumber', 'getAuthTokenSource', 'isClaudeAISubscriber', 'getAnthropicApiKeyWithSource', 'getApiKeyFromConfigOrMacOSKeychain', 'getAgentDescriptionsTotalTokens', 'AGENT_DESCRIPTIONS_THRESHOLD', 'isSupportedJetBrainsTerminal', 'getTerminalIdeType', 'isJetBrainsPluginInstalledCachedSync', 'toIDEDisplayName', body + '\nreturn getActiveNotices')(React, Box, Text, getLargeMemoryFiles, MAX_MEMORY_CHARACTER_COUNT, figures, () => row.cwd, relative, formatNumber, () => ({ source: row.authTokenSource ?? 'none' }), () => row.subscriber ?? false, () => ({ source: row.apiKeySource ?? 'none' }), () => row.storedAPIKey ? { key: 'owned-fake-key' } : null, getAgentDescriptionsTotalTokens, AGENT_DESCRIPTIONS_THRESHOLD, () => !!row.jetbrainsIDE, () => row.jetbrainsIDE || null, () => row.jetbrainsInstalled ?? false, () => row.jetbrainsIDE)
  const context = { config: { autoInstallIdeExtension: row.autoInstallIDE }, memoryFiles: row.memoryFiles ?? [], agentDefinitions: row.agentDefinitions }
  const notices = active(context)
  const text = notices.length ? await renderToAnsiString(React.createElement(AppStateProvider, null, React.createElement(Probe, null, ...notices.map((notice: any) => React.createElement(React.Fragment, { key: notice.id }, notice.render(context))))), row.columns ?? 80) : ''
  results.push({ ids: notices.map((notice: any) => notice.id), text, agentDescriptionTokens: getAgentDescriptionsTotalTokens(context.agentDefinitions) })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Notice fixture escaped boundary')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ results, layouts, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), () => resolve()))
process.exit(0)
