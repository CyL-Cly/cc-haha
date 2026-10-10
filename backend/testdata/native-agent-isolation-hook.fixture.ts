import { parse } from 'acorn'
import { basename, join } from 'node:path'
import { readFileSync, mkdirSync, writeFileSync, existsSync } from 'node:fs'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(process.env.HOME, 'actual Agent isolation hook producer')
if (process.env.CC_HAHA_AGENT_ISOLATION_FIXTURE !== '1') throw new Error('Unowned Agent isolation fixture')
await import('../../preload')
const noop = () => {}
function originalFunctions(path: string, names: string[]) {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(readFileSync(new URL(path, import.meta.url), 'utf8'))
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  return names.map(name => { const node = nodes.find((node: any) => node.id.name === name); if (!node) throw new Error('Missing original function ' + name); return source.slice(node.start, node.end) }).join('\n')
}
const hookSource = (names: string[]) => originalFunctions('../../src/utils/hooks.ts', names)
const { hookJSONOutputSchema } = await import('../../src/types/hooks')
const parsed = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', hookSource(['validateHookJson', 'parseHookOutput', 'parseHttpHookOutput']) + '\nreturn { parseHookOutput, parseHttpHookOutput }')(hookJSONOutputSchema, JSON.parse, JSON.stringify, noop)
const rows = []
for (const row of input.rows) {
  const physical = join(process.env.HOME!, row.name)
  mkdirSync(physical, { recursive: true })
  const cwd = join(physical, 'workspace'), target = join(physical, 'hook-worktree'), journal = join(physical, 'journal.json')
  mkdirSync(cwd)
  const script = join(physical, 'create.py')
  writeFileSync(script, `import sys,json,os\nvalue=json.load(sys.stdin)\njson.dump(value,open(${JSON.stringify(journal)},'w'))\nos.makedirs(${JSON.stringify(target)},exist_ok=True)\nprint(${JSON.stringify(row.fail ? '' : target)})\nsys.exit(${row.fail ? 2 : 0})\n`)
  const hooks = [{ hooks: [{ type: 'command', command: '/usr/bin/python3 ' + script }] }]
  const matching = new Function('getHooksConfig', 'logForDebugging', 'normalizeLegacyToolName', 'getLegacyToolNames', 'DEFAULT_HOOK_SHELL', 'basename', hookSource(['matchesPattern', 'hookDedupKey', 'prepareIfConditionMatcher', 'getMatchingHooks']) + '\nreturn getMatchingHooks')(() => hooks, noop, (name: string) => name, () => [], 'bash', basename)
  const outside = new Function('dependencies', `with (dependencies) { ${hookSource(['executeHooksOutsideREPL'])}; return executeHooksOutsideREPL }`)({
    isEnvTruthy: () => false, shouldDisableAllHooksIncludingManaged: () => false, shouldSkipHookDueToTrust: () => false, logForDebugging: noop, getSessionId: () => row.sid, getMatchingHooks: matching, isInternalHook: () => false, getPluginHookCounts: () => undefined, getHookTypeCounts: () => ({}), logEvent: noop, jsonStringify: JSON.stringify, logError: noop, randomUUID: () => 'private-hook', createCombinedAbortSignal: () => ({ signal: new AbortController().signal, cleanup: noop }), isAsyncHookJSONOutput: (value: any) => value.async === true, isSyncHookJSONOutput: (value: any) => value.async !== true,
    execCommandHook: async (_hook: any, ...args: any[]) => {
      const value = args.find(value => typeof value === 'string' && value.includes('hook_event_name'))
      if (!value) throw new Error('Original execCommandHook did not provide its JSON input')
      const proc = Bun.spawn(['/usr/bin/python3', script], { cwd, env: { HOME: process.env.HOME, PATH: '/usr/bin:/bin' }, stdin: new TextEncoder().encode(value), stdout: 'pipe', stderr: 'pipe' })
      const [status, stdout, stderr] = await Promise.all([proc.exited, new Response(proc.stdout).text(), new Response(proc.stderr).text()])
      return { status, stdout, stderr, aborted: false }
    }, execHttpHook: () => { throw new Error('HTTP not owned by this case') }, ...parsed,
  })
  const create = new Function('createBaseHookInput', 'executeHooksOutsideREPL', 'TOOL_HOOK_EXECUTION_TIMEOUT_MS', hookSource(['executeWorktreeCreateHook']) + '\nreturn executeWorktreeCreateHook')(() => ({ session_id: row.sid, transcript_path: '', cwd }), outside, 600000)
  const worktree = new Function('dependencies', `with (dependencies) { ${originalFunctions('../../src/utils/worktree.ts', ['agentWorktreeUnavailableReason', 'createAgentWorktreeIfSupported', 'createAgentWorktree'])}; return createAgentWorktreeIfSupported }`)({ hasWorktreeCreateHook: () => true, findCanonicalGitRoot: () => { throw new Error('Source hook must precede Git discovery') }, getCwd: () => cwd, AGENT_WORKTREE_UNAVAILABLE_REASON: 'unowned fallback', logForDebugging: noop, validateWorktreeSlug: new Function('VALID_WORKTREE_SLUG_SEGMENT', 'MAX_WORKTREE_SLUG_LENGTH', originalFunctions('../../src/utils/worktree.ts', ['validateWorktreeSlug']) + '\nreturn validateWorktreeSlug')(/^[a-zA-Z0-9._-]+$/, 64), executeWorktreeCreateHook: create })
  let result: any, error: string | undefined
  try { result = await worktree(row.slug) } catch (e: any) { error = e.message }
  if (!existsSync(journal)) throw new Error('Original hook did not execute: ' + error)
  const event = JSON.parse(readFileSync(journal, 'utf8'))
  rows.push({ name: row.name, result, error, event, target })
}
process.stdout.write(JSON.stringify({ rows }) + '\n')
