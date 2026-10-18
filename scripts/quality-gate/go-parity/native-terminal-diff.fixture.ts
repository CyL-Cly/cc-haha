import fs from 'node:fs'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original complete DiffDialog/data hooks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_DIFF_SOURCE !== '1') throw new Error('Missing original Diff guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function compile(path: string, deps: any, result: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../src/' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, path.endsWith('tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + result)(...Object.values(deps))
}
const parsers = compile('utils/gitDiff.ts', {}, '({parseGitNumstat,parseGitDiff,parseShortstat})')
const parsed = (input.parsers ?? []).map((row: any) => ({ stats: parsers.parseGitNumstat(row.numstat), short: parsers.parseShortstat(row.shortstat), hunks: parsers.parseGitDiff(row.diff) }))
let refs: any[] = [], refCursor = 0
const { useTurnDiffs } = compile('hooks/useTurnDiffs.ts', { useMemo: (run: any) => run(), useRef: (initial: any) => refs[refCursor++] ??= { current: initial } }, '({useTurnDiffs})')
const turns = (input.histories ?? []).map((history: any) => {
  refCursor = 0
  return useTurnDiffs(history)
})
const React = { createElement: (type: any, props: any, ...children: any[]) => ({ type, props: { ...props, children } }) }
const { diffWordsWithSpace } = await import('diff')
const { default: wrapText } = await import('../../../src/ink/wrap-text')
const { stringWidth } = await import('../../../src/ink/stringWidth')
const fallback = compile('components/StructuredDiff/Fallback.tsx', { React, _c: (n: number) => Array(n).fill(Symbol.for('react.memo_cache_sentinel')), useMemo: (fn: any) => fn(), useTheme: () => ['dark'], diffWordsWithSpace, wrapText, stringWidth, Box: 'Box', Text: 'Text', NoSelect: 'NoSelect' }, '({formatDiff})')
const rendered = (input.rendered ?? []).map((row: any) => fallback.formatDiff(row.hunk.lines, row.hunk.oldStart, row.width, false, row.theme).map((node: any) => {
  const spans: any[] = []
  function collect(node: any, style: any = { color: 'text', backgroundColor: '', dim: false }) {
    if (Array.isArray(node)) { node.forEach(child => collect(child, style)); return }
    if (typeof node === 'string' || typeof node === 'number') { spans.push({ ...style, text: String(node) }); return }
    if (!node?.props) return
    const next = node.type === 'Text' ? { color: node.props.color ?? style.color, backgroundColor: node.props.backgroundColor ?? style.backgroundColor, dim: node.props.dimColor ?? style.dim } : style
    collect(node.props.children, next)
  }
  collect(node); return spans
}))
const words = (input.words ?? []).map((pair: string[]) => diffWordsWithSpace(pair[0], pair[1], { ignoreCase: false }).map((part: any) => ({ value: part.value, ...(part.added ? { added: true } : {}), ...(part.removed ? { removed: true } : {}) })))
let physical: any
if (input.physical) {
  const { dirname, join } = await import('node:path')
  const { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } = await import('./client-options-capabilities')
  const absent = absentManagedSettingsFixturePaths()
  for (let parent = input.cwd; ; parent = dirname(parent)) {
    for (const name of ['.git', 'CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md']) {
      const path = join(parent, name); if (!fs.existsSync(path)) absent.push(path)
    }
    if (dirname(parent) === parent) break
  }
  const git = join(input.home, 'bin/git')
  process.chdir(input.cwd)
  const allowed = new Set(['diff\u0000HEAD\u0000--shortstat', 'diff\u0000HEAD\u0000--numstat', 'diff\u0000HEAD', 'ls-files\u0000--others\u0000--exclude-standard'])
  const caps = await installClientFixtureCapabilities(input.home, { absentPaths: absent, privateExecutableFiles: [git], approvedSpawn: request => request.command === git && request.args[0] === '--no-optional-locks' && allowed.has(request.args.slice(1).join('\u0000')) })
  // Source's execFileNoThrow omits cwd for this call. Bind Node's actual
  // inherited private cwd explicitly before the unchanged restrictive guard.
  // The underlying request still passes its executable/argv/env/fd checks.
  const processes = createRequire(import.meta.url)('node:child_process')
  const guardedSpawn = processes.spawn
  processes.spawn = (command: any, args: any, options: any) => guardedSpawn(command, args, { ...options, cwd: options?.cwd ?? process.cwd() })
  syncBuiltinESMExports()
  ;(globalThis as any).MACRO = { VERSION: '999.0.0-diff-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid' }
  await import('../../../preload')
  const state = await import('../../../src/bootstrap/state')
  state.setCwdState(input.cwd); state.setOriginalCwd(input.cwd); state.setProjectRoot(input.cwd)
  const { fetchGitDiff, fetchGitDiffHunks } = await import('../../../src/utils/gitDiff')
  let stats: any, hunks: any
  try { [stats, hunks] = await Promise.all([fetchGitDiff(), fetchGitDiffHunks()]) }
  catch (error) { throw new Error('Original Git physical error; blocked=' + JSON.stringify(caps.blocked) + '; ' + error) }
  physical = { stats, hunks, blocked: caps.blocked }
}
const dialogs = []
for (const row of input.dialogs ?? []) {
  row.current.hunks = new Map(Object.entries(row.current.hunks ?? {}))
  for (const turn of row.turns) turn.files = new Map(Object.entries(turn.files))
  let values: any[] = [], cursor = 0, bindings: any = {}, completed: any[] = [], dirty = false
  const refs: any[] = []; let refCursor = 0
  const state = (initial: any) => { const i = cursor++; values[i] ??= initial; return [values[i], (value: any) => { const next = typeof value === 'function' ? value(values[i]) : value; dirty ||= !Object.is(next, values[i]); values[i] = next }] }
  const cache = Array(73).fill(Symbol.for('react.memo_cache_sentinel'))
  const DiffDialog = compile('components/diff/DiffDialog.tsx', { React, _c: () => cache, useState: state, useRef: (initial: any) => refs[refCursor++] ??= { current: initial }, useEffect: (run: any) => run(), useDiffData: () => row.current, useTurnDiffs: () => row.turns, useRegisterOverlay: () => {}, useKeybindings: (value: any) => bindings = value, useShortcutDisplay: () => 'esc', plural: (count: number, value: string) => count === 1 ? value : value + 's', Box: 'Box', Text: 'Text', DiffDetailView: 'DiffDetailView', DiffFileList: 'DiffFileList', Dialog: 'Dialog', Byline: 'Byline' }, 'DiffDialog')
  function walk(node: any, predicate: any): any {
    if (Array.isArray(node)) { for (const n of node) { const v = walk(n, predicate); if (v) return v }; return null }
    if (!node || typeof node !== 'object') return null
    return predicate(node) ? node : walk(node.props?.children, predicate)
  }
  const observations = []
  let tree: any
  function render() { let count = 0; do { dirty = false; cursor = 0; refCursor = 0; tree = DiffDialog({ messages: [], onDone: (...args: any[]) => completed.push(args) }); if (++count > 10) throw new Error('Original Diff did not settle effects') } while (dirty) }
  function snapshot() {
    const list = walk(tree, (n: any) => n.type === 'DiffFileList'), detail = walk(tree, (n: any) => n.type === 'DiffDetailView')
    return { sourceIndex: values[2], selectedIndex: values[1], viewMode: values[0], selected: list?.props.selectedIndex ?? null, path: detail?.props.filePath ?? null, completed: structuredClone(completed) }
  }
  render(); observations.push(snapshot())
  for (const action of row.actions) {
    if (typeof action === 'object' && action.resolveCurrent) { row.current = action.resolveCurrent; row.current.hunks = new Map(Object.entries(row.current.hunks ?? {})) }
    else if (action === 'diff:dismiss') tree.props.onCancel()
    else { if (!bindings[action]) throw new Error('Missing original binding ' + action); bindings[action]() }
    render(); observations.push(snapshot())
  }
  dialogs.push(observations)
}
const normalize = (_key: string, value: any) => value instanceof Map ? Object.fromEntries(value) : value
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ parsed, turns, dialogs, rendered, words, physical }, normalize), error => error ? reject(error) : resolve()))
