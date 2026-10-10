import { resolve, join } from 'node:path'
if (process.env.CC_HAHA_INTERNAL_READ_FIXTURE !== '1') throw new Error('private fixture only')
const root = resolve(import.meta.dir, '../..')
const input = await Bun.stdin.json()
globalThis.MACRO = { VERSION: 'fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
await import(root+'/preload.ts')
const state = await import(root+'/src/bootstrap/state.ts')
state.setOriginalCwd(input.originalCwd ?? input.cwd)
state.setCwdState(input.cwd)
state.setProjectRoot(input.projectRoot ?? input.cwd)
state.switchSession(input.sid)
if (input.planSlug) state.getPlanSlugCache().set(input.sid, input.planSlug)
const filesystem = await import(root+'/src/utils/permissions/filesystem.ts')
const memories = await import(root+'/src/memdir/paths.ts')
const plans = await import(root+'/src/utils/plans.ts')
const storage = await import(root+'/src/utils/toolResultStorage.ts')
const sessions = await import(root+'/src/utils/sessionStorage.ts')
const read = (await import(root+'/src/tools/FileReadTool/FileReadTool.ts')).FileReadTool
const print = await import(root+'/src/cli/print.ts')
const permission = { mode: 'default', shouldAvoidPermissionPrompts: false, isBypassPermissionsModeAvailable: false, alwaysAllowRules: {}, alwaysAskRules: {}, alwaysDenyRules: {}, additionalWorkingDirectories: new Map() }
const rows = []
for (const row of input.rows) {
 const path = row.path.replaceAll('%BUNDLED%', filesystem.getBundledSkillsRoot())
 const value = { file_path: path }
 const context = { abortController: new AbortController(), options: { tools: [read], commands: [], mainLoopModel: 'fixture-model' }, getAppState: () => ({ toolPermissionContext: { ...permission, ...(row.permissions ?? {}), additionalWorkingDirectories: new Map() }, todos: {} }), setAppState: () => {}, messages: [] }
 const internal = filesystem.checkReadableInternalPath(path, value)
 const decision = await print.getCanUseToolFn(undefined, {} as any, () => [])(read, value, context as any, {} as any, 'fixture-read' as any)
 rows.push({ path, internal: { behavior: internal.behavior, reason: internal.decisionReason?.reason ?? null }, decision: { behavior: decision.behavior, message: decision.message ?? null } })
}
console.log(JSON.stringify({ rows, paths: { project: sessions.getProjectDir(input.cwd), sessionMemory: filesystem.getSessionMemoryDir(), plans: plans.getPlansDirectory(), slug: plans.getPlanSlug(), toolResults: storage.getToolResultsDir(), scratch: filesystem.getScratchpadDir(), projectTemp: filesystem.getProjectTempDir(), autoMemory: memories.getAutoMemPath(), bundled: filesystem.getBundledSkillsRoot() } }))
