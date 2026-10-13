import { parse } from 'acorn'
import { basename } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original outside REPL Hooks')
if (process.env.CC_HAHA_OUTSIDE_HOOK_FIXTURE !== '1') throw new Error('Unowned outside Hooks fixture')
await import('../../../../preload')
const { hookJSONOutputSchema } = await import('../../../../src/types/hooks')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../../src/utils/hooks.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n).filter((n: any) => n?.type === 'FunctionDeclaration')
const extract = (names: string[]) => names.map(name => { const n = nodes.find((n: any) => n.id.name === name); if (!n) throw new Error('Missing outside Source ' + name); return source.slice(n.start, n.end) }).join('\n')
const noop = () => {}
const parsed = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', extract(['validateHookJson', 'parseHookOutput', 'parseHttpHookOutput']) + '\nreturn {parseHookOutput,parseHttpHookOutput}')(hookJSONOutputSchema, JSON.parse, JSON.stringify, noop)
const rows = []
for (const row of input.rows) {
 const match = new Function('getHooksConfig', 'logForDebugging', 'normalizeLegacyToolName', 'getLegacyToolNames', 'DEFAULT_HOOK_SHELL', 'basename', extract(['matchesPattern', 'hookDedupKey', 'prepareIfConditionMatcher', 'getMatchingHooks']) + '\nreturn getMatchingHooks')(() => row.settings.hooks?.[row.event] ?? [], noop, (name: string) => name, () => [], 'bash', basename)
 const implementation = new Function('dependencies', `with(dependencies){${extract(['executeHooksOutsideREPL'])};return executeHooksOutsideREPL}`)({
  isEnvTruthy: () => row.simple, shouldDisableAllHooksIncludingManaged: () => row.settings.disableAllHooks, shouldSkipHookDueToTrust: () => false, logForDebugging: noop, getSessionId: () => row.sid, getMatchingHooks: match, isInternalHook: () => false, getPluginHookCounts: () => undefined, getHookTypeCounts: () => ({}), logEvent: noop, jsonStringify: JSON.stringify, logError: noop, randomUUID: () => 'fixture', createCombinedAbortSignal: () => ({ signal: new AbortController().signal, cleanup: noop }), isAsyncHookJSONOutput: (json: any) => json.async === true, isSyncHookJSONOutput: (json: any) => json.async !== true,
  execCommandHook: async (hook: any) => { const o = row.outputs[hook.command]; return { stdout: o.stdout ?? '', stderr: o.stderr ?? '', status: o.code ?? 0, aborted: o.aborted ?? false } }, execHttpHook: async (hook: any) => row.http[hook.url], ...parsed,
 })
 const fields = { session_id: row.sid, transcript_path: '/original/transcript', cwd: '/original/cwd', hook_event_name: row.event, ...row.fields }

 if(row.event==='WorktreeCreate'){
 const has=new Function('getHooksConfigFromSnapshot','getRegisteredHooks','shouldAllowManagedHooksOnly',extract(['hasWorktreeCreateHook'])+';return hasWorktreeCreateHook')(()=>row.settings.hooks,()=>null,()=>false)()
 const wrapper=new Function('createBaseHookInput','executeHooksOutsideREPL','TOOL_HOOK_EXECUTION_TIMEOUT_MS',extract(['executeWorktreeCreateHook'])+';return executeWorktreeCreateHook')(()=>({session_id:row.sid,transcript_path:'/original/transcript',cwd:'/original/cwd'}),implementation,600000)
 let result:any;try{result={path:(await wrapper(row.name)).worktreePath}}catch(e){result={error:e.message}}
 rows.push({has,...result})
 }else if ((row.event === 'PreCompact' || row.event === 'PostCompact') && row.wrapper) {
  const name = row.event === 'PreCompact' ? 'executePreCompactHooks' : 'executePostCompactHooks'
  const wrapper = new Function('createBaseHookInput','executeHooksOutsideREPL','TOOL_HOOK_EXECUTION_TIMEOUT_MS',extract([name]) + '\nreturn ' + name)(() => ({session_id:row.sid,transcript_path:'/original/transcript',cwd:'/original/cwd'}),implementation,600000)
  rows.push(await wrapper(row.event === 'PreCompact' ? {trigger:row.fields.trigger,customInstructions:row.fields.custom_instructions} : {trigger:row.fields.trigger,compactSummary:row.fields.compact_summary}))
 }else if (row.event === 'ConfigChange' && row.wrapper) {
  const wrapper = new Function('createBaseHookInput','executeHooksOutsideREPL','TOOL_HOOK_EXECUTION_TIMEOUT_MS',extract(['executeConfigChangeHooks'])+'\nreturn executeConfigChangeHooks')(() => ({session_id:row.sid,transcript_path:'/original/transcript',cwd:'/original/cwd'}),implementation,600000)
  rows.push(await wrapper(row.fields.source,row.fields.file_path))
 } else rows.push(await implementation({ hookInput: fields, matchQuery: row.matchQuery, timeoutMs: 600000, signal: row.aborted ? AbortSignal.abort() : undefined }))
}
const timeout = new Function('process', 'SESSION_END_HOOK_TIMEOUT_MS_DEFAULT', extract(['getSessionEndHookTimeoutMs']) + '\nreturn getSessionEndHookTimeoutMs')({ env: {} }, 1500)
const timeouts = input.timeouts.map((raw: string) => new Function('process', 'SESSION_END_HOOK_TIMEOUT_MS_DEFAULT', extract(['getSessionEndHookTimeoutMs']) + '\nreturn getSessionEndHookTimeoutMs')({ env: { CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS: raw } }, 1500)())
process.stdout.write(JSON.stringify({ rows, timeouts, defaultTimeout: timeout() }))
