import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original mounted StatusLine')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_STATUSLINE_SOURCE !== '1') throw new Error('Missing pinned original StatusLine guard')
const configDirectory = join(home, '.claude')
const settingsPath = join(configDirectory, 'settings.json')
fs.mkdirSync(configDirectory, { recursive: true })
fs.writeFileSync(settingsPath, JSON.stringify(input.settings))
fs.writeFileSync(join(configDirectory, '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, editorMode: input.editorMode ?? 'normal', projects: { [home]: { hasTrustDialogAccepted: input.trusted !== false } } }))
if (input.cachedSubscriber === true) {
  delete process.env.ANTHROPIC_API_KEY
  fs.writeFileSync(join(configDirectory, '.credentials.json'), JSON.stringify({ claudeAiOauth: { accessToken: 'private-statusline-subscriber', scopes: ['user:inference', 'user:profile'], expiresAt: 9999999999999, subscriptionType: 'max' } }))
}
const shell = join(home, 'private-sh')
fs.writeFileSync(shell, '#!/bin/sh\nexec /bin/sh "$@"\n', { mode: 0o700 })
const commands = new Set<string>([input.settings?.statusLine?.command, ...(input.steps ?? []).flatMap((step: any) => step.settings?.statusLine?.command ? [step.settings.statusLine.command] : [])].filter(Boolean))
const absent = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absent.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let parent = spelling; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(parent) === parent) break
}
const spawns: any[] = []
const physicalRequests: any[] = []
const api = input.apiResponses ? Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const body = await request.json()
  physicalRequests.push({ body, authorization: request.headers.get('authorization'), apiKey: request.headers.get('x-api-key') })
  const response = input.apiResponses[physicalRequests.length - 1]
  if (!response || request.method !== 'POST' || new URL(request.url).pathname !== '/v1/messages') throw new Error('Unowned original StatusLine model request')
  return new Response(response.wire, { status: response.status ?? 200, headers: response.headers })
} }) : undefined
if (api) process.env.ANTHROPIC_BASE_URL = api.url.origin
const caps = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: api?.url.origin, absentPaths: absent, privateExecutableFiles: [shell], approvedSpawn: request => request.command === shell && request.args.length === 2 && request.args[0] === '-c' && commands.has(request.args[1]!) || request.command === '/usr/bin/pgrep' && request.args.length === 2 && request.args[0] === '-P' && spawns.some(child => String(child.pid) === request.args[1]) })
const require = createRequire(import.meta.url)
const childProcess = require('node:child_process')
const guardedSpawn = childProcess.spawn
const guardedSpawnSync = childProcess.spawnSync
const otherSpawns: any[] = []
let unavailableLegacyKeychainCommand: string | undefined
const observedSpawnSync = (...args: any[]) => {
  otherSpawns.push({ command: args[0], args: args[1], stack: new Error('Source native primitive observation').stack })
  // Disposable HOME cannot isolate macOS Keychain. Substitute only this
  // exact private-config legacy read with an unavailable native OS result;
  // the original auth caller still catches it and reads its real fallback.
  if (args[0] === unavailableLegacyKeychainCommand && Array.isArray(args[1]) && args[1].length === 0) {
    caps.secureReads.push('private-legacy-keychain-unavailable')
    return { pid: 0, output: [null, Buffer.alloc(0), Buffer.from('private fixture Keychain unavailable')], stdout: Buffer.alloc(0), stderr: Buffer.from('private fixture Keychain unavailable'), status: 1, signal: null }
  }
  return guardedSpawnSync(...args)
}
// Node's shell:true primitive selects /bin/sh and default piped descriptors.
// A private launcher execs that same native /bin/sh; retain the shared guard's
// original exact executable, private env/cwd and descriptor checks unchanged.
const hookSpawn = (command: string, args: string[], options: any) => {
  if (options?.shell === true && Array.isArray(args) && args.length === 0 && commands.has(command)) {
    const child = guardedSpawn(shell, ['-c', command], { ...options, shell: false, stdio: ['pipe', 'pipe', 'pipe'] })
    spawns.push({ command, args, shell: true, pid: child.pid, at: performance.now() })
    return child
  }
  return guardedSpawn(command, args, options)
}
childProcess.spawn = hookSpawn
childProcess.spawnSync = observedSpawnSync
mock.module('child_process', () => ({ ...childProcess, spawn: hookSpawn, spawnSync: observedSpawnSync }))
syncBuiltinESMExports()
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const fallback = createFallbackStorage({ name: 'private-unavailable-keychain', read: () => null, readAsync: async () => null, update: () => ({ success: false }), delete: () => false }, plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => fallback }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-statusline-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const { getMacOsKeychainStorageServiceName } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
unavailableLegacyKeychainCommand = `security find-generic-password -a $USER -w -s "${getMacOsKeychainStorageServiceName()}"`
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
if (process.env.ANTHROPIC_API_KEY) {
  const { normalizeApiKeyForConfig } = await import('../../../src/utils/authPortable')
  config.saveGlobalConfig(previous => ({ ...previous, customApiKeyResponses: { approved: [normalizeApiKeyForConfig(process.env.ANTHROPIC_API_KEY!)], rejected: [] } }))
}
const settings = await import('../../../src/utils/settings/settings')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
const React = await import('react')
if (input.footer) {
  // These sibling renderers do not own the StatusLine registration condition
  // or lifecycle. Keep the real Footer and real StatusLine/hooks/shell intact.
  mock.module('../../../src/components/PromptInput/Notifications.tsx', () => ({ Notifications: () => null }))
  mock.module('../../../src/components/PromptInput/PromptInputFooterLeftSide.tsx', () => ({ PromptInputFooterLeftSide: () => null }))
  mock.module('../../../src/components/PromptInput/PromptInputFooterSuggestions.tsx', () => ({ PromptInputFooterSuggestions: () => null }))
  mock.module('../../../src/components/PromptInput/PromptInputHelpMenu.tsx', () => ({ PromptInputHelpMenu: () => null }))
}
const { render, Ansi, Box, Text } = await import('../../../src/ink')
if (input.rendererProfiles) {
  const { renderToAnsiString } = await import('../../../src/utils/staticRender')
  const { Parser } = await import('../../../src/ink/termio')
  const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
  const { default: wrapText } = await import('../../../src/ink/wrap-text')
  const profiles = []
  const projection = (text: string) => {
    let hyperlink: string | undefined
    const cells = []
    for (const action of new Parser().feed(text)) {
      if (action.type === 'link') hyperlink = action.action.type === 'start' ? action.action.url : undefined
      if (action.type === 'text') for (const g of action.graphemes) cells.push({ text: g.value, style: action.style, ...(hyperlink && { hyperlink }) })
    }
    return cells
  }
  for (const profile of input.rendererProfiles) {
    if (profile.links) process.env.TERM_PROGRAM = 'ghostty'
    else delete process.env.TERM_PROGRAM
    const node = React.createElement(Box, { paddingX: profile.padding }, React.createElement(Text, { dimColor: true, wrap: 'truncate' }, React.createElement(Ansi, null, profile.text)))
    const original = await renderToAnsiString(React.createElement(ThemeProvider, { initialState: profile.theme ?? 'dark' }, node), profile.width)
    const plain = new Parser().feed(profile.text).filter(action => action.type === 'text').map((action: any) => action.graphemes.map((g: any) => g.value).join('')).join('')
    profiles.push({ original, wrapPreview: wrapText(plain, profile.width - profile.padding * 2, 'truncate'), cells: projection(original), nativeCells: projection(profile.nativeRows.join('\n')) })
  }
  if (caps.blocked.length) throw new Error('Original Ansi escaped private boundary: ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ profiles, blocked: caps.blocked }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
const { StatusLine, statusLineShouldDisplay, getLastAssistantMessageId } = await import('../../../src/components/StatusLine')
const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
const { createStore } = await import('../../../src/state/store')
const initial = getDefaultAppState()
initial.settings = settings.getSettings_DEPRECATED() ?? {}
initial.mainLoopModel = input.model ?? 'fixture-statusline-model'
const store = createStore(initial)
const messagesRef = { current: input.messages ?? [] }
let vimMode = input.vimMode
let footerProps = { apiKeyStatus: 'valid', debug: false, exitMessage: { show: false }, mode: 'prompt', autoUpdaterResult: null, isAutoUpdating: false, verbose: false, onAutoUpdaterResult: () => {}, onChangeIsUpdating: () => {}, suggestions: [], selectedSuggestion: 0, toolPermissionContext: initial.toolPermissionContext, helpOpen: false, suppressHint: false, isLoading: false, tasksSelected: false, teamsSelected: false, bridgeSelected: false, tmuxSelected: false, isPasting: false, isSearching: false, historyQuery: '', setHistoryQuery: () => {}, historyFailedMatch: false, onOpenTasksDialog: () => {}, ...(input.footer ?? {}) }
const OriginalFooter = input.footer ? (await import('../../../src/components/PromptInput/PromptInputFooter')).default : undefined
const { TerminalSizeContext } = await import('../../../src/ink/components/TerminalSizeContext')
let terminalRows = input.rows ?? 40
const stdin = new PassThrough() as any
stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
const stdout = new PassThrough() as any
stdout.columns = input.width ?? 100; stdout.isTTY = false
let output = ''
stdout.on('data', (chunk: any) => output += chunk.toString())
const view = () => React.createElement(AppStoreContext.Provider, { value: store }, OriginalFooter ? React.createElement(TerminalSizeContext.Provider, { value: { columns: input.width ?? 100, rows: terminalRows } }, React.createElement(OriginalFooter, { ...footerProps, messages: messagesRef.current, vimMode })) : React.createElement(StatusLine, { messagesRef, lastAssistantMessageId: getLastAssistantMessageId(messagesRef.current), vimMode }))
const app = await render(view(), { stdin, stdout, stderr: stdout, exitOnCtrlC: false, patchConsole: false })
const receipt = join(home, 'statusline-inputs.jsonl')
const records = () => { try { return fs.readFileSync(receipt, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) } catch { return [] } }
const frames: any[] = []
const generatedNames: Array<string | null> = []
const deadline = Date.now() + 15000
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve)) }
const until = async (test: () => boolean, label: string) => { while (!test()) { if (Date.now() > deadline) throw new Error(label + ': ' + JSON.stringify({ spawns, blocked: caps.blocked, records: records(), output })); await new Promise(resolve => setTimeout(resolve, 5)) }; await flush() }
try {
  for (const step of input.steps ?? []) {
    if (step.op === 'wait') await until(() => records().length >= step.count, 'Original statusline command input')
    else if (step.op === 'text') await until(() => (store.getState().statusLineText ?? '').includes(step.value), 'Original statusline rendered command output')
    else if (step.op === 'settle') await new Promise(resolve => setTimeout(resolve, step.milliseconds))
    else if (step.op === 'state') {
      store.setState(previous => ({ ...previous, ...step.value, toolPermissionContext: step.permissionMode ? { ...previous.toolPermissionContext, mode: step.permissionMode } : previous.toolPermissionContext }))
    } else if (step.op === 'messages') { messagesRef.current = step.value; app.rerender(view()) }
    else if (step.op === 'vim') { vimMode = step.value; app.rerender(view()) }
    else if (step.op === 'footer') { footerProps = { ...footerProps, ...step.value }; if (step.rows !== undefined) terminalRows = step.rows; app.rerender(view()) }
    else if (step.op === 'child-retired') await until(() => { try { process.kill(spawns[step.index].pid, 0); return false } catch (error: any) { return error?.code === 'ESRCH' } }, 'Original StatusLine owned child retired')
    else if (step.op === 'settings') {
      fs.writeFileSync(settingsPath, JSON.stringify(step.settings)); resetSettingsCache()
      store.setState(previous => ({ ...previous, settings: settings.getSettings_DEPRECATED() ?? {} }))
    } else if (step.op === 'query') {
      const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
      const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
      const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
      const { createUserMessage } = await import('../../../src/utils/messages')
      const history = [createUserMessage({ content: step.text })]
      for await (const value of queryModelWithStreaming({ messages: history, systemPrompt: asSystemPrompt([]), thinkingConfig: { type: 'disabled' }, tools: [], signal: new AbortController().signal, options: { model: input.model ?? 'fixture-statusline-model', querySource: 'repl_main_thread', isNonInteractiveSession: false, agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext() } })) {
        if (value.type === 'assistant') history.push(value)
      }
      messagesRef.current = history
      app.rerender(view())
    } else if (step.op === 'query-name') {
      const { generateSessionName } = await import('../../../src/commands/rename/generateSessionName')
      generatedNames.push(await generateSessionName(messagesRef.current, new AbortController().signal))
    } else if (step.op === 'quota') {
      const limits = await import('../../../src/services/claudeAiLimits')
      limits.extractQuotaStatusFromHeaders(new Headers(step.headers))
    } else if (step.op === 'unmount') app.unmount()
    else throw new Error('Unknown original StatusLine step ' + step.op)
    await flush()
    frames.push({ op: step.op, records: records(), text: store.getState().statusLineText, notifications: store.getState().notifications, output })
  }
  const exited = app.waitUntilExit()
  app.unmount(); await exited
  await flush()
  if (caps.blocked.length) throw new Error('Original StatusLine escaped private boundary: ' + JSON.stringify({ blocked: caps.blocked, otherSpawns }))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ frames, records: records(), spawns, physicalRequests, generatedNames, displayed: statusLineShouldDisplay(store.getState().settings), blocked: caps.blocked }), error => error ? reject(error) : resolve()))
} finally { app.unmount(); api?.stop(true) }
process.exit(0)
