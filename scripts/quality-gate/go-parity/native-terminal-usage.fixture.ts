import fs from 'node:fs'
import { join } from 'node:path'
import { createRequire } from 'node:module'
import React from 'react'
import { c as _c } from 'react/compiler-runtime'
import { mock } from 'bun:test'
import axios, { AxiosError } from 'axios'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Original Usage consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_USAGE_FIXTURE !== '1') throw new Error('Pinned actual Source guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-usage-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('private-credentials-file')
  try { return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) } catch { return null }
} }) }))
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source native primitive')
  caps.secureReads.push('private-empty-legacy'); return null
} }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const auth = await import('../../../src/utils/auth')
const usage = await import('../../../src/services/api/usage')
const usageIndex = (await import('../../../src/commands/usage/index')).default
const { extraUsage } = await import('../../../src/commands/extra-usage/index')
const { Box, Text } = await import('../../../src/ink')
const { AppStateProvider } = await import('../../../src/state/AppState')
const { ProgressBar } = await import('../../../src/components/design-system/ProgressBar')
const { formatResetText } = await import('../../../src/utils/format')
const { formatCost } = await import('../../../src/cost-tracker')
const { renderToAnsiString } = await import('../../../src/utils/staticRender')
const requests: any[] = []
// This is the external HTTP adapter only. Original URL construction, headers,
// subscriber/profile/expiry gates, axios JSON/error handling and 5s budget run.
axios.defaults.adapter = async request => {
  requests.push({ url: request.url, method: request.method, timeout: request.timeout, headers: request.headers.toJSON() })
  const timeout = AbortSignal.timeout(request.timeout || 5000)
  const response = await fetch(input.peer + '/api/oauth/usage', { method: request.method?.toUpperCase(), headers: request.headers.toJSON() as any, signal: timeout })
  const result = { data: await response.text(), status: response.status, statusText: response.statusText, headers: {}, config: request }
  if (response.status >= 300) throw new AxiosError('Request failed with status code ' + response.status, AxiosError.ERR_BAD_REQUEST, request, undefined, result)
  return result
}
const sourceFile = new URL('../../../src/components/Settings/Usage.tsx', import.meta.url).pathname
const source = ts.createSourceFile(sourceFile, fs.readFileSync(sourceFile, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && ['LimitBar', 'ExtraUsageSection'].includes(node.name?.text) || ts.isVariableStatement(node) && node.declarationList.declarations.some((declaration: any) => declaration.name.getText(source) === 'EXTRA_USAGE_SECTION_TITLE'))
if (nodes.length !== 3) throw new Error('Original Usage function identities changed')
const body = ts.transpileModule(nodes.map((node: any) => node.getText(source)).join('\n'), { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const helpers = new Function('React', '_c', 'Box', 'Text', 'ProgressBar', 'formatResetText', 'formatCost', 'getSubscriptionType', 'extraUsageCommand', body + '\nreturn {LimitBar,ExtraUsageSection}')(React, _c, Box, Text, ProgressBar, formatResetText, formatCost, auth.getSubscriptionType, extraUsage)
const OriginalDate = globalThis.Date
if (input.now) { const fixed = new OriginalDate(input.now).getTime(); globalThis.Date = class extends OriginalDate { constructor(...args: any[]) { super(...(args.length ? args : [fixed]) as [any]) }; static now() { return fixed } } as DateConstructor }
let utilization: unknown, error: unknown
try { utilization = await usage.fetchUtilization() } catch (failure) { const e = failure as any; error = { message: e.message, body: e.response?.data } }
const renders = []
for (const row of input.render ?? []) {
  const node = row.extra ? React.createElement(helpers.ExtraUsageSection, { extraUsage: row.extra, maxWidth: row.width }) : React.createElement(helpers.LimitBar, { title: row.title, limit: row.limit, maxWidth: row.width, showTimeInReset: row.showTime, extraSubtext: row.extraSubtext })
  renders.push(await renderToAnsiString(React.createElement(AppStateProvider, null, node), row.width))
}
globalThis.Date = OriginalDate
if (caps.blocked.length) throw new Error('Usage escaped disposable boundary: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ utilization, error, requests, renders, subscription: auth.getSubscriptionType(), subscriber: auth.isClaudeAISubscriber(), hasProfile: auth.hasProfileScope(), extraEnabled: extraUsage.isEnabled(), registration: { name: usageIndex.name, kind: usageIndex.type, availability: usageIndex.availability }, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), () => resolve()))
process.exit(0)
