import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { feature } from 'bun:bundle'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Thinking summary model betas')
if (process.env.CC_HAHA_THINKING_SUMMARIES_FIXTURE !== '1') throw new Error('Unowned thinking summaries fixture')
// Readonly compiler initialization performs its native filename-case probe.
// It is test tooling; actual Source file reads and component execution below
// remain inside the disposable capability boundary.
const ts = input.uiRows ? createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript') : null
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
if (input.uiRows) {
  const declaration = (file: string, name: string) => {
    const source = ts.createSourceFile(file, readFileSync(new URL(file, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    const node = source.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === name)
    if (!node) throw new Error('Missing original component: ' + name)
    return ts.transpileModule(node.getText(source).replace(/^export\s+/, ''), { compilerOptions: { target: ts.ScriptTarget.ESNext, jsx: ts.JsxEmit.React } }).outputText
  }
  const React = { createElement: (type: any, props: any, ...children: any[]) => ({ type, props: { ...props, children } }) }
  const memo = (size: number) => Array(size).fill(Symbol.for('react.memo_cache_sentinel'))
  const primitive = Symbol('terminal-framework-primitive')
  // Only JSX/Ink container and memo-cache primitives are supplied. Both actual
  // Message block gating and the actual child thinking renderers execute.
  const thinking = new Function('_c', 'React', 'Box', 'Text', 'CtrlOToExpand', 'Markdown', declaration('../../../src/components/messages/AssistantThinkingMessage.tsx', 'AssistantThinkingMessage') + '\nreturn AssistantThinkingMessage')(memo, React, primitive, primitive, primitive, primitive)
  const redacted = new Function('_c', 'React', 'Box', 'Text', declaration('../../../src/components/messages/AssistantRedactedThinkingMessage.tsx', 'AssistantRedactedThinkingMessage') + '\nreturn AssistantRedactedThinkingMessage')(memo, React, primitive, primitive)
  const connectorTextFeature = feature('CONNECTOR_TEXT') ? true : false
  const buildFeature = (name: string) => {
    if (name !== 'CONNECTOR_TEXT') throw new Error('Undeclared component feature')
    return connectorTextFeature
  }
  const block = new Function('_c', 'React', 'feature', 'AssistantThinkingMessage', 'AssistantRedactedThinkingMessage', declaration('../../../src/components/Message.tsx', 'AssistantMessageBlock') + '\nreturn AssistantMessageBlock')(memo, React, buildFeature, thinking, redacted)
  const textOf = (node: any): string => {
    if (typeof node === 'string' || typeof node === 'number') return String(node)
    if (Array.isArray(node)) return node.map(textOf).join('')
    if (!node || typeof node !== 'object') return ''
    if (node.type === thinking || node.type === redacted) return textOf(node.type(node.props))
    return textOf(node.props?.children)
  }
  const rows = input.uiRows.map((row: any) => textOf(block({ param: { type: row.redacted ? 'redacted_thinking' : 'thinking', thinking: row.text, data: 'PRIVATE_ENCRYPTED_DATA' }, isTranscriptMode: row.transcript, verbose: false, thinkingBlockId: row.id, lastThinkingBlockId: row.lastId })))
  if (capabilities.blocked.length) throw new Error('Blocked Source component primitive: ' + capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify(rows))
  process.exit(0)
}
process.env.ANTHROPIC_API_KEY = 'fake-private-beta-key'
process.env.CI = '1'
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setAllowedSettingSources(['userSettings'])
const cache = await import('../../../src/utils/settings/settingsCache')
const { SettingsSchema } = await import('../../../src/utils/settings/types')
const { clearBetasCaches, getMergedBetas, getModelBetas } = await import('../../../src/utils/betas')
const { get3PModelCapabilityOverride } = await import('../../../src/utils/model/modelSupportOverrides')
const names = ['ANTHROPIC_BASE_URL', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS', 'DISABLE_INTERLEAVED_THINKING', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES']
const output = []
for (const row of input.rows) {
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(row.env ?? {})) {
    if (!names.includes(name) || typeof value !== 'string') throw new Error('Undeclared beta environment')
    process.env[name] = value
  }
  state.setIsInteractive(row.interactive)
  const settings = SettingsSchema().parse(row.settings ?? {})
  cache.setSessionSettingsCache({ settings, errors: [] })
  if (!row.retainCache) {
    clearBetasCaches()
    get3PModelCapabilityOverride.cache.clear()
  }
  const values = row.count ? getModelBetas(row.model) : getMergedBetas(row.model)
  output.push({ redacted: values.includes('redact-thinking-2026-02-12'), values })
}
if (capabilities.blocked.length) throw new Error('Blocked Source beta primitive: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify(output))
