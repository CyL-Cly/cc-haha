import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Web tool contract')
if (process.env.CC_HAHA_WEB_TOOLS_FIXTURE !== '1') throw new Error('Missing Web tools fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir); state.setOriginalCwd(input.homeDir); state.setProjectRoot(input.homeDir); state.setIsInteractive(false); state.setInitialMainLoopModel('deepseek-flash')
const config = await import('../../../src/utils/config'); config.enableConfigs()
const { WebFetchTool } = await import('../../../src/tools/WebFetchTool/WebFetchTool')
const { WebSearchTool } = await import('../../../src/tools/WebSearchTool/WebSearchTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const tools = { WebFetch: WebFetchTool, WebSearch: WebSearchTool }
const rows = []
for (const row of input.rows) {
 const tool = tools[row.tool as keyof typeof tools]
 const parsed = tool.inputSchema.safeParse(JSON.parse(row.input))
 const item:any = parsed.success ? {success:true,data:JSON.stringify(parsed.data)} : {success:false,issues:JSON.stringify(parsed.error.issues)}
 if (row.permission && parsed.success) item.permission = await tool.checkPermissions(parsed.data as any,{getAppState:()=>({toolPermissionContext:{...getEmptyToolPermissionContext(),...row.permission}})} as any)
 if (parsed.success) item.description = await tool.description(parsed.data as any,{} as any)
 rows.push(item)
}
const schemas = []
for (const tool of [WebFetchTool,WebSearchTool]) schemas.push(await toolToAPISchema(tool as any,{tools:[WebFetchTool,WebSearchTool],agents:[],model:'deepseek-flash',getToolPermissionContext:async()=>getEmptyToolPermissionContext()} as any))
process.stdout.write(JSON.stringify({rows,schemas,monthYear:new Date().toLocaleString('en-US',{month:'long',year:'numeric'}),capabilities}))
