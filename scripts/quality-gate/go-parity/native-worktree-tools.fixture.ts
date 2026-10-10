import { dirname,join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Worktree tool contract')
if(process.env.CC_HAHA_WORKTREE_TOOLS_FIXTURE!=='1')throw new Error('Missing Worktree fixture')
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(input.homeDir);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false);state.setInitialMainLoopModel('deepseek-flash')
const config=await import('../../../src/utils/config');config.enableConfigs()
const {EnterWorktreeTool}=await import('../../../src/tools/EnterWorktreeTool/EnterWorktreeTool')
const {ExitWorktreeTool}=await import('../../../src/tools/ExitWorktreeTool/ExitWorktreeTool')
const {toolToAPISchema}=await import('../../../src/utils/api')
const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
const tools={EnterWorktree:EnterWorktreeTool,ExitWorktree:ExitWorktreeTool}
const rows=[]
for(const row of input.rows){const tool=tools[row.tool as keyof typeof tools];const parsed=tool.inputSchema.safeParse(JSON.parse(row.input));rows.push(parsed.success?{success:true,data:JSON.stringify(parsed.data)}:{success:false,issues:JSON.stringify(parsed.error.issues)})}
const schemas=[]
for(const tool of [EnterWorktreeTool,ExitWorktreeTool])schemas.push(await toolToAPISchema(tool as any,{tools:[EnterWorktreeTool,ExitWorktreeTool],agents:[],model:'deepseek-flash',getToolPermissionContext:async()=>getEmptyToolPermissionContext()} as any))
process.stdout.write(JSON.stringify({rows,schemas,capabilities}))
