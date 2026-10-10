import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.homeDir,'Native workflow')
if(process.env.CC_HAHA_NATIVE_WORKFLOW_FIXTURE!=='1'||process.env.NODE_ENV!=='production')throw new Error('Unowned workflow fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),...(input.workflowAgentOrder?['/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token']:[])]})
const journals=[]
const preparations=[]
const observations=[]
const projections=[]
const tasks=[]
const executions=[]
let workflowTools
const syntheticOutputs=[]
const structuredExecutions=[]
let workflowPromptCommands
let projectBoundaries
const workflowAgentOrder=[]
const liveSyntheticOutputs=[]
const liveSchemaExecutions=[]
if(input.liveSchemaExecutions){
  const {createSyntheticOutputTool}=await import('../../../src/tools/SyntheticOutputTool/SyntheticOutputTool')
  const {prepareWorkflowScript,executeWorkflowScript}=await import('../../../src/utils/workflows/runtime')
  for(const row of input.liveSchemaExecutions){
    const prepared=prepareWorkflowScript("export const meta={name:'live-schema',description:'actual original schema language'};\n"+row.body)
    if(!prepared.ok)throw new Error(prepared.error)
    let calls=0
    const result=await executeWorkflowScript({vmScript:prepared.vmScript,workflowName:'live-schema',runId:'wf_live123-abc',toolUseContext:{options:{mainLoopModel:'fixture-model'},abortController:new AbortController()} as any,canUseTool:async()=>({behavior:'allow'}) as any,onAgentController:()=>{},runAgentImpl:async params=>{
      const created=createSyntheticOutputTool(params.opts?.schema as any)
      if('error'in created)throw new Error('agent({schema}): '+created.error)
      calls++;params.onAgentId('fixture-'+calls)
      const schema=JSON.parse(JSON.stringify(created.tool.inputJSONSchema))
      await created.tool.call(row.value,{} as any,async()=>({behavior:'allow'}) as any)
      return {agentId:'fixture-'+calls,value:schema,tokens:0,toolCalls:0}
    }})
    liveSchemaExecutions.push({result:result.result,error:result.error,calls})
  }
}
if(input.liveSyntheticOutputs){
  const vm=await import('node:vm')
  const {createSyntheticOutputTool}=await import('../../../src/tools/SyntheticOutputTool/SyntheticOutputTool')
  for(const row of input.liveSyntheticOutputs){
    // Only the caller's schema language expression runs in this realm. The
    // original production validator receives its actual object and getters.
    const context=vm.createContext(Object.create(null),{codeGeneration:{strings:false,wasm:false}})
    const owned=vm.runInContext(row.factory,context)
    const created=createSyntheticOutputTool(owned.schema)
    const values=[]
    if('tool'in created)for(const input of row.inputs){try{await created.tool.call(input,{} as any,async()=>({behavior:'allow'}) as any);values.push({ok:true})}catch(error){values.push({ok:false,error:error instanceof Error?error.message:String(error)})}}
    owned.mutate?.()
    liveSyntheticOutputs.push({...(('error'in created)?{error:created.error}:{}),values,reads:owned.reads?.()??0,same:createSyntheticOutputTool(owned.schema)===created})
  }
}
if(input.workflowAgentOrder){
  // This is the external credential primitive, before its eager OS prefetch.
  const {mock}=await import('bun:test')
  mock.module('../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
  const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setOriginalCwd(home);bootstrap.setCwdState(home);bootstrap.setProjectRoot(home);process.chdir(home)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {spyOn}=await import('bun:test')
  const worktrees=await import('../../../src/utils/worktree')
  const child=await import('../../../src/tools/AgentTool/runAgent')
  const {runWorkflowAgent}=await import('../../../src/utils/workflows/runWorkflowAgent')
  const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
  const {createAssistantMessage}=await import('../../../src/utils/messages')
  for(const row of input.workflowAgentOrder){
    let created=false,removed=false,idCalls=0,modelCalls=0,beforeCreation=false
    const create=spyOn(worktrees,'createAgentWorktreeIfSupported').mockImplementation((async()=>{created=true;return{worktree:{worktreePath:path.join(home,'isolated'),worktreeBranch:'private',gitRoot:home,headCommit:'head',hookBased:false}}}) as any)
    const changed=spyOn(worktrees,'hasWorktreeChanges').mockImplementation(async()=>row.dirty??false)
    const remove=spyOn(worktrees,'removeAgentWorktree').mockImplementation((async()=>{removed=true}) as any)
    const run=spyOn(child,'runAgent').mockImplementation((()=>{modelCalls++;return(async function*(){yield createAssistantMessage({content:[{type:'text',text:'actual-source-child'}]})})()}) as any)
    let error
    try{await runWorkflowAgent({prompt:'actual isolated child',opts:{isolation:'worktree'},toolUseContext:{options:{tools:[],agentDefinitions:{activeAgents:[],allAgents:[]}},getAppState:()=>({toolPermissionContext:getEmptyToolPermissionContext(),mcp:{tools:[]}})} as any,canUseTool:async()=>({behavior:'allow'}) as any,runId:'wf_order-abc',abortController:new AbortController(),onAgentId:()=>{idCalls++;beforeCreation=!created;if(row.rejectIdentity)throw new Error('identity journal rejected')},onProgress:()=>{}})}catch(cause){error=cause instanceof Error?cause.message:String(cause)}
    workflowAgentOrder.push({beforeCreation,idCalls,created,removed,modelCalls,...(error?{error}:{})})
    create.mockRestore();changed.mockRestore();remove.mockRestore();run.mockRestore()
  }
}
if(input.projectBoundaries){
  const bootstrap=await import('../../../src/bootstrap/state')
  const {getProjectDirsUpToHome}=await import('../../../src/utils/markdownConfigLoader')
  projectBoundaries=[]
  for(const row of input.projectBoundaries){
    const cwd=path.join(home,row.cwd),root=path.join(home,row.projectRoot)
    bootstrap.setOriginalCwd(cwd);bootstrap.setCwdState(cwd);bootstrap.setProjectRoot(root)
    projectBoundaries.push(getProjectDirsUpToHome('workflows',cwd))
  }
}
if(input.workflowPromptCommands){
  const cwd=path.join(home,'project')
  fs.mkdirSync(path.join(cwd,'.git'),{recursive:true})
  const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setOriginalCwd(cwd);bootstrap.setCwdState(cwd);bootstrap.setProjectRoot(cwd);process.chdir(cwd)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {getWorkflowCommands}=await import('../../../src/tools/WorkflowTool/createWorkflowCommand')
  workflowPromptCommands=[]
  for(const command of await getWorkflowCommands(cwd)){
    const {type,name,description,progressMessage,contentLength,argNames,source}=command as any
    const prompts=[]
    for(const args of input.workflowPromptCommands.args)prompts.push(await(command as any).getPromptForCommand(args))
    workflowPromptCommands.push({metadata:{type,name,description,progressMessage,contentLength,argNames,source},prompts})
  }
}
if(input.structuredExecutions){
  const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setOriginalCwd(home);bootstrap.setCwdState(home);bootstrap.setProjectRoot(home);process.chdir(home)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {createSyntheticOutputTool}=await import('../../../src/tools/SyntheticOutputTool/SyntheticOutputTool')
  const {createAssistantMessage}=await import('../../../src/utils/messages')
  const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
  const {runToolUse}=await import('../../../src/services/tools/toolExecution')
  for(const row of input.structuredExecutions){
    const created=createSyntheticOutputTool(row.schema)
    if('error'in created)throw new Error(created.error)
    const tool=row.mapError?{...created.tool,mapToolResultToToolResultBlockParam:()=>{throw new Error('actual mapper failed')}}:created.tool
    let state={toolPermissionContext:getEmptyToolPermissionContext(),sessionHooks:new Map(),tasks:{},mcp:{clients:[],tools:[],resources:{}}}
    const use={type:'tool_use' as const,id:'structured-call',name:tool.name,input:row.value}
    const assistant=createAssistantMessage({content:[use]})
    const context={options:{tools:[tool],mcpClients:[],isNonInteractiveSession:true},abortController:new AbortController(),messages:[],getAppState:()=>state,setAppState:(update:any)=>{state=update(state)},setInProgressToolUseIDs:()=>{},agentId:row.agentId,preserveToolUseResults:row.preserveToolUseResults??true} as any
    const updates=[]
    for await(const update of runToolUse(use,assistant,async()=>({behavior:'allow',updatedInput:row.value}),context)){
      const {uuid,timestamp,...message}=update.message
      if(message.type==='user')delete message.sourceToolAssistantUUID
      updates.push(message)
    }
    structuredExecutions.push(updates)
  }
}
if(input.syntheticOutputs){
  const {createSyntheticOutputTool}=await import('../../../src/tools/SyntheticOutputTool/SyntheticOutputTool')
  for(const row of input.syntheticOutputs){
    const result=createSyntheticOutputTool(row.schema)
    if('error'in result){syntheticOutputs.push({error:result.error});continue}
    const values=[]
    for(const value of row.inputs){try{const output=await result.tool.call(value,{} as any,async()=>({behavior:'allow'}) as any);values.push({ok:true,data:output.data,structured:output.structured_output})}catch(error){values.push({ok:false,error:error instanceof Error?error.message:String(error)})}}
    const same=createSyntheticOutputTool(row.schema)===result
    syntheticOutputs.push({same,values})
  }
}
if(input.workflowTools){
  const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setOriginalCwd(home);bootstrap.setCwdState(home);bootstrap.setProjectRoot(home)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {WorkflowTool}=await import('../../../src/tools/WorkflowTool/WorkflowTool')
  const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
  const {z}=await import('zod/v4')
  const parsed=input.workflowTools.inputs.map((value:any)=>{const result=WorkflowTool.inputSchema.safeParse(value);return result.success?{ok:true,data:result.data}:{ok:false,issues:result.error.issues}})
  const permissions=[]
  for(const row of input.workflowTools.permissions){
    const permission={...getEmptyToolPermissionContext(),...row.context}
    const result=await WorkflowTool.checkPermissions(row.input,{getAppState:()=>({toolPermissionContext:permission,ultracode:row.ultracode??false}),options:{isNonInteractiveSession:row.nonInteractive??false}} as any)
    permissions.push(result)
  }
  workflowTools={hasValidator:typeof WorkflowTool.validateInput==='function',schema:z.toJSONSchema(WorkflowTool.inputSchema),prompt:await WorkflowTool.prompt(),parsed,permissions,mapped:input.workflowTools.results.map((value:any)=>WorkflowTool.mapToolResultToToolResultBlockParam(value,'actual-workflow-id'))}
}
if(input.executions){
  const {spyOn}=await import('bun:test');const clock=spyOn(Date,'now').mockReturnValue(1700000000000)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {prepareWorkflowScript,executeWorkflowScript}=await import('../../../src/utils/workflows/runtime')
  const {WorkflowJournal,workflowCacheKey}=await import('../../../src/utils/workflows/journal')
  const {createWorkflowSharedCounters}=await import('../../../src/utils/workflows/harness')
  const {createLimiter}=await import('../../../src/utils/workflows/limiter')
  for(const row of input.executions){
    const prepared=prepareWorkflowScript(row.script);if(!prepared.ok)throw new Error(prepared.error)
    const events:any[]=[];const calls:any[]=[];const controllers:any[]=[]
    let spent=row.spent??0
    const journalPath=path.join(home,'execution-journals',`${executions.length}.jsonl`)
    const journal=row.journal?new WorkflowJournal(journalPath):undefined
    if(journal&&row.seed){
      let key=''
      for(const entry of row.seed){key=workflowCacheKey(entry.prompt,entry.options??{},key);await journal.append(entry.started?{type:'started',key,agentId:entry.agentId}:{type:'result',key,agentId:entry.agentId,result:entry.result})}
    }
    const snapshot=await journal?.load()
    const shared=createWorkflowSharedCounters();if(row.concurrency)shared.limiter=createLimiter(row.concurrency)
    const runAgentImpl=async(params:any)=>{
      const agentId=`fixture-${params.workflow.agentIndex}`
      calls.push({prompt:params.prompt,options:params.opts,workflow:params.workflow})
      if(row.detailed){params.onAgentId(agentId);params.onProgress({tokens:3,toolCalls:1,lastToolName:'Read'})}
      if(row.spendPerAgent)spent+=row.spendPerAgent
      if(row.agentError)throw new Error(row.agentError)
      return {agentId,value:params.prompt,tokens:row.detailed?3:0,toolCalls:row.detailed?1:0}
    }
    const toolUseContext={options:{mainLoopModel:'fixture-model'},getAppState:()=>({toolPermissionContext:{mode:'default'}})} as any
    const tokenBudget={total:row.budget??null,getTurnSpent:()=>spent}
    const onAgentController=(key:string,controller:any)=>controllers.push({key,active:controller!==undefined})
    let runNestedWorkflow,agentSpy
    if(row.nested){
      const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setOriginalCwd(home);bootstrap.setCwdState(home);bootstrap.setProjectRoot(home);process.chdir(home)
      fs.mkdirSync(path.join(home,'.git'),{recursive:true})
      fs.mkdirSync(path.join(home,'.claude','workflows'),{recursive:true})
      for(const[name,script]of Object.entries(row.nested))fs.writeFileSync(path.join(home,'.claude','workflows',`${name}.js`),String(script))
      const runAgentModule=await import('../../../src/utils/workflows/runWorkflowAgent')
      agentSpy=spyOn(runAgentModule,'runWorkflowAgent').mockImplementation(runAgentImpl)
      const {createNestedWorkflowRunner}=await import('../../../src/tools/WorkflowTool/runNestedWorkflow')
      runNestedWorkflow=createNestedWorkflowRunner({toolUseContext,canUseTool:async()=>({behavior:'allow'}) as any,runId:'wf_source123',shared,tokenBudget,journal,onProgress:event=>events.push(event),onAgentController})
    }
    const result=await executeWorkflowScript({vmScript:prepared.vmScript,toolUseContext,canUseTool:async()=>({behavior:'allow'}) as any,runId:'wf_source123',workflowName:prepared.meta.name,args:row.args,seedPhaseTitles:row.phases,tokenBudget,journal,journalSnapshot:snapshot,shared,onProgress:event=>events.push(event),onAgentController,runAgentImpl,runNestedWorkflow})
    agentSpy?.mockRestore()
    executions.push(row.detailed?{outcome:result,events,calls,controllers,journal:journal&&fs.existsSync(journalPath)?fs.readFileSync(journalPath,'utf8'):''}:result)
  }
  clock.mockRestore()
}
if(input.tasks){
  const {spyOn}=await import('bun:test')
  const clock=spyOn(Date,'now').mockReturnValue(1700000000000)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const original=await import('../../../src/tasks/LocalWorkflowTask/LocalWorkflowTask')
  for(const row of input.tasks){
    let task=original.registerWorkflowTask(row.params)
    const outputPath=task.outputFile
    const snapshots=[]
    const clean=(value:any)=>{const {abortController,agentControllers,...rest}=value;return rest}
    snapshots.push(clean(task))
    for(const batch of row.batches??[]){
      let app:any={tasks:{[task.id]:task}}
      original.updateWorkflowProgressBatch(task.id,batch,(update:any)=>{app=update(app)})
      task=app.tasks[task.id]
      snapshots.push(clean(task))
    }
    tasks.push({snapshots,outputPath,resume:original.buildResumePrompt(task),notifications:(row.notifications??[]).map((value:any)=>original.buildWorkflowNotification({...value,taskId:task.id}))})
  }
  clock.mockRestore()
}
let pluginCache
if(input.projections){
  const {readWorkflowTranscriptProjection}=await import('../../../src/server/services/workflowTranscriptProjection')
  for(const [index,row]of input.projections.entries()){
    const file=path.join(home,`projection-${index}.jsonl`)
    let previous:string|undefined
    let tick=0
    const values=[]
    for(const op of row){
      if(op.kind==='append')fs.appendFileSync(file,op.text)
      else if(op.kind==='write')fs.writeFileSync(file,op.text)
      else if(op.kind==='replace'){fs.unlinkSync(file);fs.writeFileSync(file,op.text)}
      else if(op.kind!=='read')throw new Error('Unknown projection operation')
      if(op.kind!=='read')fs.utimesSync(file,new Date(1700000000000+tick),new Date(1700000000000+tick++))
      else {
        const result=await readWorkflowTranscriptProjection(file)
        values.push({messages:result.messages,complete:result.complete,readBytes:result.readBytes,sourceChanged:previous!==undefined&&previous!==result.sourceVersion,sourceVersionPresent:!!result.sourceVersion})
        previous=result.sourceVersion
      }
    }
    projections.push(values)
  }
}
if(input.operations){
  const privatePath=(value:string)=>{
    const resolved=path.resolve(home,value)
    if(!resolved.startsWith(home+path.sep))throw new Error('Workflow fixture path escaped')
    return resolved
  }
  for(const row of input.files??[]){
    const file=privatePath(row.path)
    fs.mkdirSync(path.dirname(file),{recursive:true})
    if(row.directory)fs.mkdirSync(file,{recursive:true})
    else if(row.link)fs.symlinkSync(privatePath(row.link),file)
    else fs.writeFileSync(file,row.text.replaceAll('<private-home>',home))
    if(row.mtime!==undefined)fs.utimesSync(file,new Date(row.mtime),new Date(row.mtime))
  }
  const state=await import('../../../src/bootstrap/state')
  state.setOriginalCwd(privatePath(input.cwd??'project'))
  state.setCwdState(privatePath(input.cwd??'project'))
  state.setProjectRoot(privatePath(input.cwd??'project'))
  process.chdir(privatePath(input.cwd??'project'))
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {workflowService}=await import('../../../src/server/services/workflowService')
  if(input.inspectPluginCache){const {loadAllPluginsCacheOnly}=await import('../../../src/utils/plugins/pluginLoader');const cached=await loadAllPluginsCacheOnly();pluginCache={keys:Object.keys(cached),enabled:cached.enabled.length,pluginsPresent:Object.hasOwn(cached,'plugins')}}
  for(const op of input.operations){
    try{
      let result
      const cwd=op.cwd===undefined?undefined:privatePath(op.cwd)
      switch(op.kind){
        case 'list':result=await workflowService.listDefinitions(cwd);break
        case 'get':result=await workflowService.getDefinition(op.name,cwd);break
        case 'validate':result=workflowService.validate(op.script);break
        case 'save':result=await workflowService.saveDefinition({script:op.script,scope:op.scope??'user',cwd,name:op.name});break
        case 'delete':await workflowService.deleteDefinition(op.name,op.scope??'user',cwd);result=null;break
        case 'runs':result=await workflowService.listRuns({sessionId:op.sessionId,limit:op.limit});break
        case 'run':result=await workflowService.getRun(op.sessionId,op.runId);break
        case 'reconstruct':result=await workflowService.reconstructSessionRuns(op.sessionId);break
        case 'agent-state':result=await workflowService.getAgentRunState(op.sessionId,op.agentId);break
        default:throw new Error('Unknown workflow observation')
      }
      observations.push({result})
    }catch(e:any){observations.push({status:e.statusCode??500,code:e.code,error:e.message})}
  }
}
if(input.preparations){
  const {prepareWorkflowScript}=await import('../../../src/utils/workflows/runtime')
  const {parseWorkflowScript,usesBannedNondeterminism}=await import('../../../src/utils/workflows/meta')
  for(const value of input.preparations){
    const script=value
    const prepared=prepareWorkflowScript(script)
    if(!prepared.ok){preparations.push({ok:false,error:prepared.error});continue}
    const parsed=parseWorkflowScript(script)
    if('error' in parsed)throw new Error('Original preparation/parser inconsistent')
    preparations.push({ok:true,meta:prepared.meta,body:parsed.scriptBody,banned:usesBannedNondeterminism(parsed.scriptBody)})
  }
}
if(input.journals){
  const {WorkflowJournal,workflowCacheKey}=await import('../../../src/utils/workflows/journal')
  for(const [index,row] of input.journals.entries()){
    const file=path.join(home,`journal-${index}.jsonl`)
    if(row.seed!==undefined)fs.writeFileSync(file,row.seed)
    const journal=new WorkflowJournal(file)
    const pending=[]
    for(const value of row.entries??[])pending.push(journal.append(JSON.parse(value)))
    await Promise.all(pending);await journal.flush()
    let snapshot,error
    try{const loaded=await journal.load();snapshot={results:[...loaded.results],started:[...loaded.started]}}catch(e:any){error=e.message}
    journals.push({file:fs.existsSync(file)?fs.readFileSync(file,'utf8'):null,snapshot,error,keys:(row.keys??[]).map((r:any)=>workflowCacheKey(JSON.parse(r.prompt),r.opts===undefined?undefined:JSON.parse(r.opts),JSON.parse(r.previous)))})
  }
}
process.stdout.write(JSON.stringify({journals,preparations,observations,projections,tasks,executions,pluginCache,workflowTools,syntheticOutputs,structuredExecutions,workflowPromptCommands,projectBoundaries,workflowAgentOrder,liveSyntheticOutputs,liveSchemaExecutions,capabilities}))
