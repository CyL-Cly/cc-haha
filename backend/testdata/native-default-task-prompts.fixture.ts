import { resolve,join } from 'node:path'
import { feature } from 'bun:bundle'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text()) as {home:string,rows:Array<{env:Record<string,string>,argv?:string[],cachedFeatures?:unknown}>}
if(process.env.CC_HAHA_DEFAULT_TASK_PROMPTS_FIXTURE!=='1'||!feature('TRANSCRIPT_CLASSIFIER')||process.env.HOME!==input.home)throw new Error('Private pinned Source conditions required')
await import('../../preload')
const root=resolve(import.meta.dir,'../..')
const config=await import(join(root,'src/utils/config'));config.enableConfigs()
const {getPrompt:create}=await import(join(root,'src/tools/TaskCreateTool/prompt'))
const {getPrompt:list}=await import(join(root,'src/tools/TaskListTool/prompt'))
const {isAgentSwarmsEnabled}=await import(join(root,'src/utils/agentSwarmsEnabled'))
const {AgentTool}=await import(join(root,'src/tools/AgentTool/AgentTool'))
const {z}=await import('zod/v4')
const keys=['USER_TYPE','CC_HAHA_AGENT_TEAMS_ENABLED','CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS','CC_HAHA_AGENT_TEAMS_DEFAULT']
const rows=input.rows.map(row=>{for(const name of keys)delete process.env[name];Object.assign(process.env,row.env);process.argv=[process.execPath,import.meta.path,...(row.argv??[])];config.saveGlobalConfig(old=>({...old,cachedGrowthBookFeatures:row.cachedFeatures as any}));return{enabled:isAgentSwarmsEnabled(),create:create(),list:list()}})
const schema=z.toJSONSchema(AgentTool.inputSchema,{target:'draft-7'}) as any
const bootstrap=await import(join(root,'src/bootstrap/state'));bootstrap.setCwdState(input.home);bootstrap.setOriginalCwd(input.home);bootstrap.setProjectRoot(input.home);bootstrap.setInitialMainLoopModel('deepseek-flash');bootstrap.setMainLoopModelOverride('deepseek-flash')
process.env.ANTHROPIC_BASE_URL='http://127.0.0.1:1'
const {TaskCreateTool}=await import(join(root,'src/tools/TaskCreateTool/TaskCreateTool'))
const {TaskListTool}=await import(join(root,'src/tools/TaskListTool/TaskListTool'))
const {toolToAPISchema}=await import(join(root,'src/utils/api'))
const {clearToolSchemaCache}=await import(join(root,'src/utils/toolSchemaCache'))
const {getEmptyToolPermissionContext}=await import(join(root,'src/Tool'))
const cache=[]
const tools=[TaskCreateTool,TaskListTool]
for(const step of [{enabled:true,clear:true},{enabled:false,clear:false},{enabled:false,clear:true}]){
 process.env.USER_TYPE='external';process.env.CC_HAHA_AGENT_TEAMS_ENABLED=step.enabled?'1':'0'
 if(step.clear)clearToolSchemaCache()
 const descriptions=await Promise.all(tools.map(tool=>toolToAPISchema(tool,{tools,agents:[],model:'deepseek-flash',getToolPermissionContext:async()=>getEmptyToolPermissionContext()})))
 cache.push(descriptions.map(tool=>tool.description))
}
console.log(JSON.stringify({rows,mode:schema.properties.mode.enum,cache}))
