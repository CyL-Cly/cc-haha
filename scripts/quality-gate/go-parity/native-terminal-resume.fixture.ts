import fs from 'node:fs'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual whole /resume command and storage')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_RESUME_SOURCE !== '1') throw new Error('Missing actual resume guard')
fs.mkdirSync(join(home, 'bin'), { recursive: true })
const git = join(home, 'bin/git')
fs.writeFileSync(git, '#!/bin/sh\nexit 1\n', { mode: 0o700 })
process.env.PATH = join(home, 'bin') + ':/usr/bin:/bin'
const absent = absentManagedSettingsFixturePaths()
for (let parent = home; ; parent = dirname(parent)) {
  for (const name of ['.git', 'CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md']) {
    const path = join(parent, name)
    if (!fs.existsSync(path)) absent.push(path)
  }
  if (dirname(parent) === parent) break
}
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const spawnObservations: any[] = []
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, privateExecutableFiles: [git], approvedSpawn: request => request.command === git && ['worktree\0list\0--porcelain', '--no-optional-locks\0worktree\0list\0--porcelain'].includes(request.args.join('\0')) })
const processes = createRequire(import.meta.url)('node:child_process')
const guardedSpawn = processes.spawn
processes.spawn = (command: any, args: any, options: any) => { spawnObservations.push({command,args,cwd:options?.cwd,envPath:options?.env?.PATH,uid:options?.uid,gid:options?.gid,shell:options?.shell}); return guardedSpawn(command, args, { ...options, cwd: options?.cwd ?? process.cwd() }) }
const { spyOn } = await import('bun:test')
spyOn(await import('node:child_process'), 'spawn').mockImplementation(processes.spawn)
syncBuiltinESMExports()
process.chdir(home)
;(globalThis as any).MACRO = { VERSION: '999.0.0-resume-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setIsInteractive(true); state.switchSession(input.currentSessionId, null)
const storage = await import('../../../src/utils/sessionStorage')
for (const row of input.files) {
  const path = storage.getTranscriptPathForSession(row.sid)
  fs.mkdirSync(dirname(path), { recursive: true })
  fs.writeFileSync(path, row.text.replaceAll('$CWD', home), { mode: 0o600 })
  fs.utimesSync(path, row.modified / 1000, row.modified / 1000)
}
const path = new URL('../../../src/commands/resume/resume.tsx', import.meta.url)
const source = ts.createSourceFile(path.pathname, fs.readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
let values: any[] = [], cursor = 0, effectIndex = 0, callbackIndex = 0
const callbackMemos = new Map<number, {fn: any, deps: any[]}>()
const effects = new Map<number, any[]>()
const React = {
  createElement: (type: any, props: any, ...children: any[]) => ({ type, props: { ...props, children } }),
  useState: (initial: any) => { const slot = cursor++; if (!(slot in values)) values[slot] = initial; return [values[slot], (next: any) => values[slot] = typeof next === 'function' ? next(values[slot]) : next] },
  useCallback: (fn: any, deps: any[]) => { const slot = callbackIndex++; const previous = callbackMemos.get(slot); if (previous && deps.every((v, i) => previous.deps[i] === v)) return previous.fn; callbackMemos.set(slot, {fn, deps}); return fn },
  useEffect: (fn: any, deps: any[]) => { const slot = effectIndex++; const previous = effects.get(slot); if (!previous || deps.some((v, i) => previous[i] !== v)) { effects.set(slot, deps); fn() } },
}
const { getWorktreePaths } = await import('../../../src/utils/getWorktreePaths')
const { validateUuid } = await import('../../../src/utils/uuid')
const { checkCrossProjectResume } = await import('../../../src/utils/crossProjectResume')
const deps = { React, _c: (n: number) => Array(n).fill(Symbol.for('react.memo_cache_sentinel')), chalk: { bold: (s: string) => s }, figures: { pointer: '❯' }, ...storage, getOriginalCwd: state.getOriginalCwd, getSessionId: state.getSessionId, getWorktreePaths, validateUuid, checkCrossProjectResume, logError: () => {}, LogSelector: 'LogSelector', MessageResponse: 'MessageResponse', Spinner: 'Spinner', Box: 'Box', Text: 'Text', useIsInsideModal: () => false, useTerminalSize: () => ({ rows: 40, columns: 120 }), setClipboard: async () => { throw new Error('Unexpected cross-project clipboard') }, agenticSessionSearch: () => { throw new Error('Frozen deep search disabled') } }
const { call, filterResumableSessions } = new Function(...Object.keys(deps), js + '\nreturn {call,filterResumableSessions}')(...Object.values(deps))
let resumed: any, callbacks: any[] = []
let done: () => void
const completion = new Promise<void>(resolve => done = resolve)
const onDone = (...args: any[]) => { callbacks.push(args); done() }
let node: any
try { node = await call(onDone, { resume: async (sid: string, log: any, entrypoint: string) => { resumed = { sid, log, entrypoint } } }, input.argument) } catch (error) { throw new Error('Actual source call blocked='+JSON.stringify(caps.blocked)+'; spawn='+JSON.stringify(spawnObservations)+'; '+error) }
let logs: any[] | undefined
let renameReload: any = null
let previewCapture: any = null
if (node?.type?.name === 'ResumeCommand') {
  const deadline = Date.now() + 10000
  let tree: any
  do {
    cursor = 0; effectIndex = 0; callbackIndex = 0; tree = node.type(node.props)
    if (tree.type === 'LogSelector' || callbacks.length) break
    if (Date.now() > deadline) throw new Error('Original async resume catalog did not settle; blocked='+JSON.stringify(caps.blocked)+'; spawn='+JSON.stringify(spawnObservations))
    await new Promise<void>(resolve => setImmediate(resolve))
  } while (true)
  logs = tree.type === 'LogSelector' ? tree.props.logs : []
  if (input.rename && logs.length) {
    const beforeTitle = logs[0].customTitle
    await storage.saveCustomTitle(logs[0].sessionId, input.rename, logs[0].fullPath)
    // This is ResumeCommand's actual onLogsChanged consumer, with the real
    // saveCustomTitle disk producer. The loading branch removes LogSelector.
    const reload = tree.props.onLogsChanged()
    cursor = 0; effectIndex = 0; callbackIndex = 0
    const loadingTree = node.type(node.props)
    if (loadingTree.type === 'LogSelector') throw new Error('Original reload did not unmount LogSelector')
    await reload
    cursor = 0; effectIndex = 0; callbackIndex = 0; tree = node.type(node.props)
    if (tree.type !== 'LogSelector') throw new Error('Original reload failed to remount LogSelector')
    logs = tree.props.logs
    renameReload = { beforeTitle, loadingComponent: loadingTree.type, afterTitle: logs[0].customTitle }
  }
  if (input.previewCapture && logs.length) {
    const selected = logs[input.select ?? 0]
    const originalPreviewPath = new URL('../../../src/components/SessionPreview.tsx', import.meta.url)
    const originalPreview = ts.createSourceFile(originalPreviewPath.pathname, fs.readFileSync(originalPreviewPath, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    const previewBody = originalPreview.statements.filter((value: any) => !ts.isImportDeclaration(value)).map((value: any) => value.getText(originalPreview).replace(/^export /, '')).join('\n')
    const previewJS = ts.transpileModule(previewBody, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
    const { getAllBaseTools } = await import('../../../src/tools')
    const { formatRelativeTimeAgo } = await import('../../../src/utils/format')
    const handlers = new Map<string, () => void>()
    const previewDeps = { React, _c: (n: number) => Array(n).fill(Symbol.for('react.memo_cache_sentinel')), ...storage, getAllBaseTools, formatRelativeTimeAgo, useKeybinding: (action: string, handler: () => void) => handlers.set(action, handler), Box: 'Box', Text: 'Text', Byline: 'Byline', ConfigurableShortcutHint: 'ConfigurableShortcutHint', KeyboardShortcutHint: 'KeyboardShortcutHint', LoadingState: 'LoadingState', Messages: 'Messages' }
    const { SessionPreview } = new Function(...Object.keys(previewDeps), previewJS + '\nreturn {SessionPreview}')(...Object.values(previewDeps))
    // This is a new SessionPreview mount, with its own React state. The
    // parent onSelect remains the actual ResumeCommand closure above.
    values = []; effects.clear(); callbackMemos.clear()
    const props = { log: selected, onExit: () => {}, onSelect: tree.props.onSelect }
    const deadline = Date.now() + 10000
    do {
      cursor = 0; effectIndex = 0; callbackIndex = 0
      SessionPreview(props)
      if (values[0] !== null) break
      if (Date.now() > deadline) throw new Error('Actual original preview did not finish its real full log read')
      await new Promise<void>(resolve => setImmediate(resolve))
    } while (true)
    const captured = values[0]
    const late = { type: 'user', uuid: 'actual-external-after-preview', parentUuid: selected.sessionId + '-answer', sessionId: selected.sessionId, timestamp: '2026-10-01T00:00:02.000Z', cwd: home, message: { role: 'user', content: 'EXTERNAL_AFTER_PREVIEW_UNSEEN' } }
    fs.appendFileSync(selected.fullPath, JSON.stringify(late) + '\n')
    const fresh = await storage.loadFullLog(selected)
    // Confirmation uses the original registered keyboard consumer. It passes
    // fullLog ?? log into the actual parent handler without another read.
    handlers.get('confirm:yes')!()
    await completion
    previewCapture = { captured, fresh, physicalChanged: fs.readFileSync(selected.fullPath, 'utf8').includes('EXTERNAL_AFTER_PREVIEW_UNSEEN') }
  }
  else if (input.select !== undefined && logs.length) await tree.props.onSelect(logs[input.select])
  else if (input.cancel && logs.length) tree.props.onCancel()
} else if (node?.type?.name === 'ResumeError') {
  cursor = 0; effectIndex = 0; callbackIndex = 0; node.type(node.props)
}
if (resumed || node?.type?.name === 'ResumeError' || input.cancel) await completion
if (caps.blocked.length) throw new Error('Actual whole resume blocked ' + caps.blocked.join(','))
const output = { resumed: resumed ?? null, callbacks, logs: logs ?? null, renameReload, previewCapture, filtered: filterResumableSessions(input.filterLogs ?? [], input.currentSessionId), caps }
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(output), error => error ? reject(error) : resolve()))
