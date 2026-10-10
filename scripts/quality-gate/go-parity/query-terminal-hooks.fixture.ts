import fs from 'node:fs'
import { dirname, join, relative } from 'node:path'
import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'query terminal hooks original profile')
const keys = ['home', 'name', 'files', 'simple', 'interactive', 'trust', 'querySource', 'agentID', 'operations', 'supported', 'historySources', 'assistantSource']
if (Object.keys(input).some(key => !keys.includes(key)) || Bun.version !== '1.3.14' || !feature('TRANSCRIPT_CLASSIFIER') || process.env.CC_HAHA_QUERY_TERMINAL_HOOKS !== '1' || process.env.NODE_ENV !== 'production' || !Array.isArray(input.operations) || !Array.isArray(input.historySources) || typeof input.assistantSource !== 'string') throw new Error('Query hooks require original declared release inputs before imports')
const sources = ['userSettings', 'projectSettings', 'localSettings']
if (Object.entries(input.files ?? {}).some(([name, value]) => !sources.includes(name) || typeof value !== 'string') || input.simple !== undefined && typeof input.simple !== 'string' || input.interactive !== undefined && typeof input.interactive !== 'boolean' || input.trust !== undefined && typeof input.trust !== 'boolean') throw new Error('Query hooks reject undeclared original files/env before imports')
if (input.files !== undefined && (!input.files || Array.isArray(input.files) || typeof input.files !== 'object') || input.querySource !== undefined && !['sdk', 'repl_main_thread', 'agent'].includes(input.querySource) || input.agentID !== undefined && (typeof input.agentID !== 'string' || !/^[a-zA-Z0-9-]+$/.test(input.agentID)) || input.supported !== undefined && typeof input.supported !== 'boolean' || input.historySources.some((raw: unknown) => typeof raw !== 'string' || !JSON.parse(raw as string) || Array.isArray(JSON.parse(raw as string))) || !JSON.parse(input.assistantSource) || Array.isArray(JSON.parse(input.assistantSource))) throw new Error('Query hooks reject undeclared original context before imports')
const operations = ['snapshot', 'reset', 'update', 'write', 'register', 'session', 'clearRegistry', 'clearSession', 'programmatic', 'post', 'failure', 'normal', 'cache', 'cleanup']
for (const operation of input.operations) if (!operation || Object.keys(operation).some(key => !['kind', 'event', 'source', 'raw', 'sid', 'mode', 'value'].includes(key)) || !operations.includes(operation.kind) || operation.source && !sources.includes(operation.source) || operation.event && !['Stop', 'StopFailure', 'SubagentStop'].includes(operation.event) || operation.mode && !['array', 'matcher', 'active'].includes(operation.mode)) throw new Error('Query hooks reject prepared or unknown operations before imports')
for (const operation of input.operations) {
  if (['write'].includes(operation.kind) && (!sources.includes(operation.source) || typeof operation.raw !== 'string') || ['register', 'session'].includes(operation.kind) && (!['Stop', 'StopFailure', 'SubagentStop'].includes(operation.event) || !['array', 'matcher', 'active'].includes(operation.mode)) || ['session', 'clearSession'].includes(operation.kind) && (typeof operation.sid !== 'string' || !/^[a-zA-Z0-9-]+$/.test(operation.sid)) || operation.value !== undefined && (operation.kind !== 'cleanup' || typeof operation.value !== 'boolean')) throw new Error('Query hooks reject malformed operation before imports')
}
const project = join(home, 'project')
const paths: Record<string, string> = { userSettings: join(home, '.claude/settings.json'), projectSettings: join(project, '.claude/settings.json'), localSettings: join(project, '.claude/settings.local.json') }
fs.mkdirSync(join(home, '.claude'), { recursive: true }); fs.mkdirSync(join(project, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/.claude.json'), '{}')
for (const [name, raw] of Object.entries(input.files ?? {})) fs.writeFileSync(paths[name], raw as string)
const absentGit: string[] = []
for (const base of [home, fs.realpathSync(home)]) for (let path = dirname(base);; path = dirname(path)) { absentGit.push(join(path, '.git')); if (dirname(path) === path) break }
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), ...absentGit] })
process.env.CLAUDE_CODE_SIMPLE = input.simple ?? '1'
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(project); bootstrap.setOriginalCwd(project); bootstrap.setProjectRoot(project); bootstrap.switchSession('hook-main'); bootstrap.setIsInteractive(input.interactive ?? false); bootstrap.setSessionTrustAccepted(input.trust ?? false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const settings = await import('../../../src/utils/settings/settings')
const snapshot = await import('../../../src/utils/hooks/hooksConfigSnapshot')
const hooks = await import('../../../src/utils/hooks')
const post = await import('../../../src/utils/hooks/postSamplingHooks')
const stop = await import('../../../src/query/stopHooks')
const fork = await import('../../../src/utils/forkedAgent')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const cleanup = await import('../../../src/utils/computerUse/cleanup')
const daemon = await import('../../../src/utils/computerUse/cuHelperDaemon')
const lock = await import('../../../src/utils/computerUse/computerUseLock')
const badge = await import('../../../src/utils/computerUse/winCursorBadge')
const { clearSessionHooks } = await import('../../../src/utils/hooks/sessionHooks')
let state = getDefaultAppState()
let uuidCalls = 0
let phases: string[] = []
let allocations: string[] = []
spyOn(crypto, 'randomUUID').mockImplementation(() => { uuidCalls++; allocations.push('uuid'); return `hook-id-${uuidCalls}` as ReturnType<typeof crypto.randomUUID> })
const RealDate = Date
globalThis.Date = class extends RealDate { constructor(...args: any[]) { if (!args.length) allocations.push('date'); super(...(args.length ? args : [1700000000000]) as [number]) }; static now() { return 1700000000000 } } as DateConstructor
const history = input.historySources.map((raw: string) => JSON.parse(raw)), assistant = JSON.parse(input.assistantSource)
const system = asSystemPrompt(['original system']), userContext = {}, systemContext = {}
const context: any = { options: { tools: [], isNonInteractiveSession: !input.interactive, mainLoopModel: 'deepseek-flash', mcpClients: [] }, agentId: input.agentID, agentType: input.agentID ? 'fixture-agent' : undefined, abortController: new AbortController(), getAppState: () => state, setAppState: (fn: any) => { state = fn(state) }, updateAttributionState: () => {}, messages: history }
const identities = new Map<object, number>()
const identity = (value: object | null) => { if (!value) return null; if (!identities.has(value)) identities.set(value, identities.size + 1); return identities.get(value) }
const viewSnapshot = () => { const value = snapshot.getHooksConfigFromSnapshot(); return { raw: JSON.stringify(value), identity: identity(value) } }
const registry = () => { const value = bootstrap.getRegisteredHooks(); return value ? Object.fromEntries(Object.entries(value).map(([event, matchers]: any) => [event, matchers.map((m: any) => ({ hooks: m.hooks.map((h: any) => h.type), matcher: m.matcher ?? null }))])) : null }
const results: any[] = []
for (const operation of input.operations) {
  phases = []; allocations = []
  let value: any = null
  const active = { type: 'callback', callback: async () => { phases.push('active-callback'); return {} } }
  if (operation.kind === 'snapshot') value = viewSnapshot()
  else if (operation.kind === 'reset') snapshot.resetHooksConfigSnapshot()
  else if (operation.kind === 'update') snapshot.updateHooksConfigSnapshot()
  else if (operation.kind === 'write') fs.writeFileSync(paths[operation.source], operation.raw)
  else if (operation.kind === 'register') bootstrap.registerHookCallbacks({ [operation.event]: operation.mode === 'array' ? [] : [{ matcher: '', hooks: operation.mode === 'active' ? [active] : [] }] })
  else if (operation.kind === 'session') { const store = state.sessionHooks.get(operation.sid) ?? { hooks: {} }; store.hooks[operation.event] = operation.mode === 'array' ? [] : [{ matcher: '', hooks: operation.mode === 'active' ? [{ hook: { type: 'function', callback: async () => { phases.push('active-function'); return true }, errorMessage: 'original pending hook' } }] : [] }]; state.sessionHooks.set(operation.sid, store) }
  else if (operation.kind === 'clearRegistry') bootstrap.clearRegisteredHooks()
  else if (operation.kind === 'clearSession') clearSessionHooks(context.setAppState, operation.sid)
  else if (operation.kind === 'programmatic') post.registerPostSamplingHook(async () => { phases.push('active-programmatic') })
  else if (operation.kind === 'post') { phases.push('start'); const pending = post.executePostSamplingHooks([...history, assistant], system, userContext, systemContext, context, input.querySource ?? 'sdk'); phases.push('returned-promise'); await pending; phases.push('completed') }
  else if (operation.kind === 'failure') { phases.push('start'); const pending = hooks.executeStopFailureHooks(assistant, context); phases.push('returned-promise'); await pending; phases.push('completed') }
  else if (operation.kind === 'cleanup') {
    phases.push('start')
    if (operation.value) context.abortController.abort('original-caller-reason')
    if (!input.agentID) { const pending = cleanup.cleanupComputerUseAfterTurn(context); phases.push('returned-promise'); await pending }
    value = { inactive: !daemon.isOverlayShown() && !lock.isLockHeldLocally() && !badge.__cursorBadgeIsRunning() && !(state.computerUseMcpState?.hiddenDuringTurn?.size), canceled: context.abortController.signal.aborted }
    phases.push('completed')
  }
  else if (operation.kind === 'normal') { phases.push('start'); const generator = stop.handleStopHooks(history, [assistant], system, userContext, systemContext, context, input.querySource ?? 'sdk', false); const yielded: string[] = []; for (;;) { const next = await generator.next(); if (next.done) { value = { yielded, result: next.value }; break }; yielded.push(JSON.stringify(next.value)) }; phases.push('completed') }
  else if (operation.kind === 'cache') { const saved = fork.getLastCacheSafeParams(); value = saved ? { messages: saved.forkContextMessages.map(m => JSON.stringify(m)), systemIdentity: saved.systemPrompt === system, userIdentity: saved.userContext === userContext, contextIdentity: saved.toolUseContext === context, assistantIdentity: saved.forkContextMessages.at(-1) === assistant } : null }
  results.push({ operation: operation.kind, value, phases, allocations, uuidCalls, registry: registry(), session: [...state.sessionHooks].map(([sid, store]: any) => [sid, Object.fromEntries(Object.entries(store.hooks).map(([event, list]: any) => [event, list.map((m: any) => ({ matcher: m.matcher, hooks: m.hooks.map((h: any) => h.hook.type) }))]))]) })
}
process.stdout.write(JSON.stringify({ name: input.name, results, sourceOnly: { capabilities, settings: sources.map(source => [source, settings.getSettingsForSource(source)]), runtime: { bun: Bun.version, revision: Bun.revision }, paths: Object.fromEntries(Object.entries(paths).map(([key, value]) => [key, relative(home, value)])) } }))
