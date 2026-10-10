import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original hook command output')
if (process.env.CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE !== '1') throw new Error('Missing hook fixture')
await import('../../../preload')
const { hookJSONOutputSchema } = await import('../../../src/types/hooks')
// Generic declaration serialization: types/constraints, never executable
// hook business or captured callback output, become the native parser input.
function recipe(schema: any): any {
  const def = schema._zod.def
  const value: Record<string, any> = { type: def.type }
  for (const key of ['values', 'entries', 'defaultValue', 'discriminator']) if (key in def) value[key] = def[key]
  if (def.shape) value.shape = Object.fromEntries(Object.entries(def.shape).map(([key, child]) => [key, recipe(child)]))
  for (const key of ['innerType', 'element', 'keyType', 'valueType', 'catchall']) if (def[key]) value[key] = recipe(def[key])
  if (def.options) value.options = def.options.map(recipe)
  if (def.getter) value.inner = recipe(def.getter())
  if (def.checks?.length) value.checks = def.checks.map((check: any) => check._zod.def)
  return value
}
if(input.taskEventWrappers) {
  const {parse}=await import('acorn')
  const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/hooks.ts',import.meta.url)).text()),ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
  const functions=ast.body.map((node:any)=>node.type==='ExportNamedDeclaration'?node.declaration:node).filter((node:any)=>node?.type==='FunctionDeclaration')
  const names=['createBaseHookInput','executeTaskCreatedHooks','executeTaskCompletedHooks','executeTeammateIdleHooks','getTaskCreatedHookMessage','getTaskCompletedHookMessage','getTeammateIdleHookMessage']
  const bodies=names.map(name=>{const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('Missing actual task event Source '+name);return source.slice(node.start,node.end)}).join('\n')
  const executor=functions.find((n:any)=>n.id.name==='executeHooks'),loop=executor?.body.body.find((n:any)=>n.type==='ForOfStatement'&&n.await)
  if(!loop)throw new Error('Missing actual event completion loop')
  const allocation=(attachment:any)=>({type:'attachment',attachment}),noop=()=>{}
  const outputFns=new Function('hookJSONOutputSchema','jsonParse','jsonStringify','logForDebugging','createAttachmentMessage',functions.filter((n:any)=>['validateHookJson','parseHookOutput','processHookJSONOutput'].includes(n.id.name)).map((n:any)=>source.slice(n.start,n.end)).join('\n')+'\nreturn {parseHookOutput,processHookJSONOutput}')(hookJSONOutputSchema,JSON.parse,JSON.stringify,noop,allocation)
  const promises=executor?.body.body.find((n:any)=>n.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name==='hookPromises'))?.declarations.find((d:any)=>d.id.name==='hookPromises')?.init?.arguments?.[0]
  if(!promises)throw new Error('Missing actual command hook completion producer')
  const commandProducer=new Function('dependencies',`with(dependencies){return (${source.slice(promises.start,promises.end)})}`)
  const rows=[]
  for(const row of input.taskEventWrappers){
    const messages:any[]=[],captured:any[]=[]
    const aggregateFactory=new Function('all','createAttachmentMessage','logForDebugging','getHookDisplayText','getSessionId','getSessionHookCallback','logError',`return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${source.slice(loop.start,loop.end)}}`)(async function* (values:any[]){for(const value of values)yield value},allocation,noop,(h:any)=>h.command,()=>row.base.session_id,()=>undefined,noop)
    const run=async function* (options:any){captured.push(options.hookInput);if(row.aborted)return;const output=row.output,hook=output.type==='prompt'?output.hook:{type:'command',command:output.command};const responses:any[]=[];const producer=commandProducer({signal:undefined,timeoutMs:600000,messages:undefined,toolUseContext:undefined,hookName:row.event,hookEvent:row.event,toolUseID:row.eventID,createCombinedAbortSignal:()=>({signal:new AbortController().signal,cleanup:noop}),randomUUID:()=>row.eventID,getHookDisplayText:(h:any)=>h.statusMessage||h.command||h.prompt,getJsonInput:()=>({ok:true,value:JSON.stringify(options.hookInput)}),boundRequestPrompt:undefined,forceSyncExecution:false,emitHookStarted:noop,emitHookResponse:(value:any)=>responses.push(value),execCommandHook:async()=>({stdout:output.stdout,stderr:output.stderr??'',output:output.stdout+(output.stderr??''),status:output.code??0,aborted:output.aborted??false}),createAttachmentMessage:allocation,parseHookOutput:outputFns.parseHookOutput,processHookJSONOutput:outputFns.processHookJSONOutput,isAsyncHookJSONOutput:(value:any)=>value.async===true,isSyncHookJSONOutput:(value:any)=>value.async!==true,chalk:{bold:(value:any)=>value},errorMessage:(error:any)=>error.message});const values=[];for await(const value of producer({hook},0))values.push(value);row.actualOutcomes=values.map(v=>v.outcome);row.responseOutcomes=responses.map(v=>v.outcome);yield* aggregateFactory(values,row.event,row.event,row.eventID)}
    const original=new Function('getSessionId','getTranscriptPathForSession','getCwd','getMainThreadAgentType','randomUUID','executeHooks','TOOL_HOOK_EXECUTION_TIMEOUT_MS',bodies+'\nreturn {executeTaskCreatedHooks,executeTaskCompletedHooks,executeTeammateIdleHooks,getTaskCreatedHookMessage,getTaskCompletedHookMessage,getTeammateIdleHookMessage}')(()=>row.base.session_id,()=>row.base.transcript_path,()=>row.base.cwd,()=>row.base.agent_type,()=>row.eventID,run,600000)
    const generator=row.event==='TeammateIdle'?original.executeTeammateIdleHooks(row.teammateName,row.teamName,row.permissionMode):original[row.event==='TaskCreated'?'executeTaskCreatedHooks':'executeTaskCompletedHooks'](row.taskID,row.taskSubject,row.taskDescription,row.teammateName,row.teamName,row.permissionMode,undefined,undefined,{agentId:row.agentId})
    for await(const frame of generator){if(frame.message){const message={...frame.message,attachment:{...frame.message.attachment}};delete message.attachment.durationMs;messages.push({message})}else messages.push(frame)}
    const format=original[row.event==='TaskCreated'?'getTaskCreatedHookMessage':row.event==='TaskCompleted'?'getTaskCompletedHookMessage':'getTeammateIdleHookMessage']
    rows.push({input:captured[0],frames:messages,feedback:format(row.blockingError),outcomes:row.actualOutcomes,responseOutcomes:row.responseOutcomes})
  }
  process.stdout.write(JSON.stringify({rows}))
}
else if(input.bashIfMatchers) {
  const {parse}=await import('acorn')
  const source=new Bun.Transpiler({loader:'tsx'}).transformSync(await Bun.file(new URL('../../../src/tools/BashTool/BashTool.tsx',import.meta.url)).text())
  const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'}),properties:any[]=[]
  const walk=(node:any)=>{if(!node||typeof node!=='object')return;if(node.type==='Property'&&node.key.name==='preparePermissionMatcher')properties.push(node);for(const value of Object.values(node)){if(Array.isArray(value))value.forEach(walk);else if(value&&typeof value==='object')walk(value)}};walk(ast)
  if(properties.length!==1)throw new Error('Actual Bash permission matcher missing')
  const {getParserModule}=await import('../../../src/utils/bash/bashParser'),{parseForSecurityFromAst}=await import('../../../src/utils/bash/ast'),{permissionRuleExtractPrefix,matchWildcardPattern}=await import('../../../src/utils/permissions/shellRuleMatching')
  const parser=getParserModule();if(!parser)throw new Error('Actual pure Bash parser missing')
  const rows=[]
  for(const row of input.bashIfMatchers){const original=new Function('parseForSecurity','permissionRuleExtractPrefix','matchWildcardPattern',`return ({${source.slice(properties[0].start,properties[0].end)}}).preparePermissionMatcher`)(async(command:string)=>command.length===0?{kind:'simple',commands:[]}:row.enabled&&command.length<=10000?parseForSecurityFromAst(command,parser.parse(command,Infinity)):{kind:'parse-unavailable'},permissionRuleExtractPrefix,matchWildcardPattern);const matcher=await original({command:row.command});rows.push(row.patterns.map(matcher))}
  process.stdout.write(JSON.stringify({rows}))
}
else if(input.bashSemantics) {
  const {checkSemantics}=await import('../../../src/utils/bash/ast')
  process.stdout.write(JSON.stringify({rows:input.bashSemantics.map((commands:any)=>checkSemantics(commands))}))
}
else if(input.bashSecurity) {
  const {getParserModule}=await import('../../../src/utils/bash/bashParser')
  const {parseForSecurityFromAst,checkSemantics}=await import('../../../src/utils/bash/ast')
  const {PARSE_ABORTED}=await import('../../../src/utils/bash/parser')
  const parser=getParserModule();if(!parser)throw new Error('Actual pure Bash parser missing')
  const rows=input.bashSecurity.map((row:any)=>{const result=parseForSecurityFromAst(row.source,row.aborted?PARSE_ABORTED:parser.parse(row.source,Infinity));return {result,...(result.kind==='simple'?{semantics:checkSemantics(result.commands)}:{})}})
  process.stdout.write(JSON.stringify({rows}))
}
else if(input.bashSyntax) {
  const {getParserModule}=await import('../../../src/utils/bash/bashParser')
  const parser=getParserModule();if(!parser)throw new Error('Actual pure Bash parser missing')
  process.stdout.write(JSON.stringify({rows:input.bashSyntax.map((source:string)=>parser.parse(source,Infinity))}))
}
else if(input.bashLexer) {
  const {parse}=await import('acorn')
  const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/bash/bashParser.ts',import.meta.url)).text()),ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
  const names=new Set(['makeLexer','advance','peek','byteAt','isWordChar','isWordStart','isIdentStart','isIdentChar','isDigit','isHexDigit','isBaseDigit','isHeredocDelimChar','skipBlanks','nextToken','byteLengthUtf8'])
  const bodies=ast.body.map((node:any)=>node.type==='ExportNamedDeclaration'?node.declaration:node).filter((node:any)=>node?.type==='FunctionDeclaration'&&names.has(node.id.name)).map((node:any)=>source.slice(node.start,node.end))
  if(bodies.length!==names.size)throw new Error('Actual bash lexer Source missing')
  const original=new Function(bodies.join('\n')+'\nreturn {makeLexer,nextToken,byteAt,byteLengthUtf8}')()
  const rows=[];for(const row of input.bashLexer){const lexer=original.makeLexer(row.source),tokens=[];for(let i=0;i<100000;i++){const token=original.nextToken(lexer,row.position);tokens.push(token);if(token.type==='EOF')break}rows.push({tokens,i:lexer.i,b:lexer.b,bytes:original.byteLengthUtf8(row.source),byteTable:Array.from({length:row.source.length+1},(_,i)=>original.byteAt(lexer,i))})}
  process.stdout.write(JSON.stringify({rows}))
}
else if(input.wildcards) {
  const {matchWildcardPattern}=await import('../../../src/utils/permissions/shellRuleMatching')
  process.stdout.write(JSON.stringify({rows:input.wildcards.map((row:any)=>matchWildcardPattern(row.pattern,row.value))}))
}
else if(input.ifMatchers) {
  const {parse}=await import('acorn')
  const {absentManagedSettingsFixturePaths,installClientFixtureCapabilities}=await import('./client-options-capabilities')
  const {join,dirname}=await import('node:path'),{realpathSync}=await import('node:fs')
  const absent=[...absentManagedSettingsFixturePaths()]
  for(const home of new Set([input.homeDir,realpathSync(input.homeDir)]))for(let dir=dirname(home);;dir=dirname(dir)){absent.push(join(dir,'.git'));if(dir===dirname(dir))break}
  const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
  const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir);bootstrap.setIsInteractive(false)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {FileReadTool}=await import('../../../src/tools/FileReadTool/FileReadTool'),{FileWriteTool}=await import('../../../src/tools/FileWriteTool/FileWriteTool'),{FileEditTool}=await import('../../../src/tools/FileEditTool/FileEditTool'),{GlobTool}=await import('../../../src/tools/GlobTool/GlobTool'),{GrepTool}=await import('../../../src/tools/GrepTool/GrepTool'),{AgentTool}=await import('../../../src/tools/AgentTool/AgentTool'),{BashTool}=await import('../../../src/tools/BashTool/BashTool')
  const {findToolByName}=await import('../../../src/Tool'),{normalizeLegacyToolName,permissionRuleValueFromString}=await import('../../../src/utils/permissions/permissionRuleParser')
  const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/hooks.ts',import.meta.url)).text()),ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
  const node=ast.body.find((n:any)=>n.type==='FunctionDeclaration'&&n.id.name==='prepareIfConditionMatcher');if(!node)throw new Error('Actual if matcher absent')
  const prepare=new Function('normalizeLegacyToolName','findToolByName','permissionRuleValueFromString',source.slice(node.start,node.end)+'\nreturn prepareIfConditionMatcher')(normalizeLegacyToolName,findToolByName,permissionRuleValueFromString)
  const tools=[FileReadTool,FileWriteTool,FileEditTool,GlobTool,GrepTool,AgentTool,BashTool]
  const rows=[];for(const row of input.ifMatchers){const matcher=await prepare({hook_event_name:row.event,tool_name:row.name,tool_input:row.input},tools);rows.push(matcher?row.conditions.map(matcher):null)}
  process.stdout.write(JSON.stringify({rows,capabilities}))
}
else if(input.toolFacades) {
  const {parse}=await import('acorn')
  const hooksCode=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/hooks.ts',import.meta.url)).text())
  const facadesCode=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/services/tools/toolHooks.ts',import.meta.url)).text())
  const declarations=(source:string)=>{const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'});return ast.body.map((node:any)=>node.type==='ExportNamedDeclaration'?node.declaration:node).filter((node:any)=>node?.type==='FunctionDeclaration')}
  const hookFunctions=declarations(hooksCode),facadeFunctions=declarations(facadesCode)
  const extract=(source:string,nodes:any[],names:string[])=>names.map(name=>{const node=nodes.find(n=>n.id.name===name);if(!node)throw new Error('Missing actual Hook function '+name);return source.slice(node.start,node.end)}).join('\n')
  const executor=hookFunctions.find(n=>n.id.name==='executeHooks')
  const loop=executor?.body.body.find((n:any)=>n.type==='ForOfStatement'&&n.await)
  if(!loop)throw new Error('Missing actual command completion aggregation loop')
  const allocation=(attachment:any)=>({type:'attachment',attachment})
  const noop=()=>{}
  const outputs=new Function('hookJSONOutputSchema','jsonParse','jsonStringify','logForDebugging','createAttachmentMessage',extract(hooksCode,hookFunctions,['validateHookJson','parseHookOutput','processHookJSONOutput'])+'\nreturn {parseHookOutput,processHookJSONOutput}')(hookJSONOutputSchema,JSON.parse,JSON.stringify,noop,allocation)
  const aggregateFactory=new Function('all','createAttachmentMessage','logForDebugging','getHookDisplayText','getSessionId','getSessionHookCallback','logError',`return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${hooksCode.slice(loop.start,loop.end)}}`)(async function* (values:any[]){for(const value of values)yield value},allocation,noop,(hook:any)=>hook.command,()=>input.sessionID,()=>undefined,noop)
  const observations=[]
  for(const row of input.toolFacades){
    const tool={name:row.toolName,isMcp:row.isMCP},context:any={getAppState:()=>({toolPermissionContext:{mode:'bypassPermissions'}}),abortController:new AbortController()}
    const makeExecutor=(event:string)=>async function* (..._args:any[]){const hookPromises=(row.outputs[event]??[]).map((output:any)=>{const parsed=outputs.parseHookOutput(output.stdout);if(!parsed.json||parsed.validationError)throw new Error('Fixture requires actual validated JSON command output');return {...outputs.processHookJSONOutput({json:parsed.json,command:output.command,hookName:event+':'+row.toolName,toolUseID:row.toolUseID,hookEvent:event,expectedHookEvent:event,stdout:output.stdout,stderr:'',exitCode:0,durationMs:42}),hook:{type:'command',command:output.command},outcome:'success'}});yield* aggregateFactory(hookPromises,event,event+':'+row.toolName,row.toolUseID)}
    const functions=new Function('executePreToolHooks','executePostToolHooks','executePostToolUseFailureHooks','createAttachmentMessage','logForDebugging','getPreToolHookBlockingMessage','getRuleBehaviorDescription','getStatsStore','logEvent','logError','sanitizeToolNameForAnalytics','formatError','isMcpTool',extract(facadesCode,facadeFunctions,['runPreToolUseHooks','runPostToolUseHooks','runPostToolUseFailureHooks'])+'\nreturn {runPreToolUseHooks,runPostToolUseHooks,runPostToolUseFailureHooks}')(makeExecutor('PreToolUse'),makeExecutor('PostToolUse'),makeExecutor('PostToolUseFailure'),allocation,noop,(name:string,error:any)=>`${name} hook error: ${error.blockingError}`,(behavior:string)=>behavior==='allow'?'allowed':behavior==='deny'?'denied':'asked for confirmation for',()=>undefined,noop,noop,(name:string)=>name,(error:any)=>error.message,(tool:any)=>tool.isMcp)
    const before=[],after=[],pre=[]
    for await(const result of functions.runPreToolUseHooks(context,tool,row.input,row.toolUseID,'actual-message',undefined,undefined,undefined)){pre.push(result);if(result.type==='message'||result.type==='additionalContext')before.push(result.message.message.attachment)}
    const post=[]
    if(row.executePost)for await(const result of functions.runPostToolUseHooks(context,tool,row.toolUseID,'actual-message',row.input,row.response,undefined,undefined,undefined)){post.push(result);if(result.message)after.push(result.message.attachment)}
    const failure=[];if(row.executeFailure)for await(const result of functions.runPostToolUseFailureHooks(context,tool,row.toolUseID,'actual-message',row.input,row.error,row.isInterrupt,undefined,undefined,undefined)){failure.push(result);if(result.message)after.push(result.message.attachment)}
    if(pre.some((value:any)=>value.type==='preventContinuation'&&value.shouldPreventContinuation)&&row.executePost){const reason=pre.filter((value:any)=>value.type==='stopReason').at(-1)?.stopReason||'Execution stopped by hook';after.push({type:'hook_stopped_continuation',message:reason,hookName:'PreToolUse:'+row.toolName,toolUseID:row.toolUseID,hookEvent:'PreToolUse'})}
    const strip=(value:any)=>{const copy={...value};delete copy.durationMs;return copy}
    observations.push({attachments:[...before,...after].filter(Boolean).map(strip),pre,post,...(row.executeFailure?{failure}:{})})
  }
  process.stdout.write(JSON.stringify({observations}))
}
else if(input.hookRules) {
  const {absentManagedSettingsFixturePaths,installClientFixtureCapabilities}=await import('./client-options-capabilities')
  const {join,dirname}=await import('node:path')
  const {realpathSync}=await import('node:fs')
  const absent=[...absentManagedSettingsFixturePaths()]
  for(const home of new Set([input.homeDir,realpathSync(input.homeDir)])) {
    for(let dir=dirname(home);;dir=dirname(dir)){absent.push(join(dir,'.git'));if(dir===dirname(dir))break}
  }
  const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
  const bootstrap=await import('../../../src/bootstrap/state')
  bootstrap.setCwdState(input.hookRules.cwd);bootstrap.setOriginalCwd(input.hookRules.cwd);bootstrap.setProjectRoot(input.hookRules.cwd);bootstrap.setIsInteractive(false);bootstrap.switchSession(input.hookRules.sessionID)
  const config=await import('../../../src/utils/config');config.enableConfigs()
  const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
  const {FileReadTool}=await import('../../../src/tools/FileReadTool/FileReadTool')
  const {FileWriteTool}=await import('../../../src/tools/FileWriteTool/FileWriteTool')
  const {checkRuleBasedPermissions}=await import('../../../src/utils/permissions/permissions')
  const rows=[]
  for(const row of input.hookRules.rows){const permission={...getEmptyToolPermissionContext(),...row.permission};const tool=row.name==='Read'?FileReadTool:FileWriteTool;const context:any={getAppState:()=>({toolPermissionContext:permission}),abortController:new AbortController()};rows.push(await checkRuleBasedPermissions(tool,row.input,context))}
  process.stdout.write(JSON.stringify({rows,capabilities}))
}
else if(input.pathSafety) {
  const { parse }=await import('acorn')
  const {join,resolve,sep}=await import('node:path')
  const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/permissions/filesystem.ts',import.meta.url)).text())
  const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
  const names=new Set(['normalizeCaseForComparison','isClaudeSettingsPath','isClaudeConfigFilePath','isUncPath','isDangerousFilePathToAutoEdit','hasSuspiciousWindowsPathPattern','checkPathSafetyForAutoEdit'])
  const bodies=[]
  const constants=[]
  for(let node of ast.body){if(node.type==='ExportNamedDeclaration')node=node.declaration;if(node?.type==='FunctionDeclaration'&&names.has(node.id.name))bodies.push(source.slice(node.start,node.end));if(node?.type==='VariableDeclaration'&&node.declarations.some((d:any)=>['DANGEROUS_FILES','DANGEROUS_DIRECTORIES'].includes(d.id.name)))constants.push(source.slice(node.start,node.end))}
  if(bodies.length!==7||constants.length!==2)throw new Error('Actual path safety Source unavailable')
  const readonlyCode=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/shell/readOnlyCommandValidation.ts',import.meta.url)).text())
  const readonlyAST:any=parse(readonlyCode,{ecmaVersion:'latest',sourceType:'module'})
  let uncBody=''
  for(let node of readonlyAST.body){if(node.type==='ExportNamedDeclaration')node=node.declaration;if(node?.type==='FunctionDeclaration'&&node.id.name==='containsVulnerableUncPath')uncBody=readonlyCode.slice(node.start,node.end)}
  if(!uncBody)throw new Error('Actual UNC Source unavailable')
  const rows=[]
  for(const profile of input.pathSafety){const cwd=profile.cwd;const expandPath=(path:string)=>resolve(cwd,path);const paths=profile.forms;const pathInWorkingPath=(path:string,root:string)=>{const norm=(s:string)=>resolve(cwd,s).replace(/^\/private\/var\//,'/var/').replace(/^\/private\/tmp(\/|$)/,'/tmp$1').toLowerCase();const p=norm(path),r=norm(root);return p===r||p.startsWith(r+sep)}
    const production=new Function('expandPath','sep','join','getOriginalCwd','getSettingsPaths','getPlatform','getPathsForPermissionCheck','pathInWorkingPath',`${constants.join('\n')}\n${uncBody}\n${bodies.join('\n')}\nreturn checkPathSafetyForAutoEdit`)(expandPath,sep,join,()=>cwd,()=>profile.settingsPaths,()=>profile.platform,()=>paths,pathInWorkingPath)
    rows.push(production(profile.path,paths,{allowUncPath:profile.allowUNC}))
  }
  process.stdout.write(JSON.stringify({rows}))
}
else if(input.httpHelpers) {
  const { parse } = await import('acorn')
  const source = new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/hooks/execHttpHook.ts',import.meta.url)).text())
  const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
  const names=new Set(['urlMatchesPattern','sanitizeHeaderValue','interpolateEnvVars'])
  const bodies=[]
  for(const node of ast.body) if(node.type==='FunctionDeclaration'&&names.has(node.id.name)) bodies.push(source.slice(node.start,node.end))
  if(bodies.length!==3) throw new Error('Actual HTTP Hook helpers unavailable')
  const production=new Function('logForDebugging','process',`${bodies.join('\n')}\nreturn {urlMatchesPattern,interpolateEnvVars}`)(()=>{},{env:input.httpHelpers.environment})
  const {isBlockedAddress}=await import('../../../src/utils/hooks/ssrfGuard')
  process.stdout.write(JSON.stringify({urls:input.httpHelpers.urls.map((row:any)=>production.urlMatchesPattern(row.target,row.pattern)),headers:input.httpHelpers.headers.map((row:any)=>production.interpolateEnvVars(row.value,new Set(row.allowed))),addresses:input.httpHelpers.addresses.map(isBlockedAddress)}))
}
else if (input.shells) {
  const { formatShellPrefixCommand } = await import('../../../src/utils/bash/shellPrefix')
  process.stdout.write(JSON.stringify({rows:input.shells.map((row:any)=>formatShellPrefixCommand(row.prefix,row.command))}))
}
else if(input.powershell) {
  const { findPowerShell } = await import('../../../src/utils/shell/powershellDetection')
  const rows = []
  for(const profile of input.powershell) {
    rows.push(await findPowerShell())
  }
  process.stdout.write(JSON.stringify({rows}))
}
else if (input.selection) {
  const { parse } = await import('acorn')
  const extract = async (path: string, names: string[]) => {
    const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
    const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
    const bodies = []
    for (let node of ast.body) {
      if (node.type === 'ExportNamedDeclaration') node = node.declaration
      if (node?.type === 'FunctionDeclaration' && names.includes(node.id.name)) bodies.push(source.slice(node.start, node.end))
    }
    if (bodies.length !== names.length) throw new Error('Actual Hook selection Source unavailable')
    return bodies.join('\n')
  }
  const snapshot = await extract('../../../src/utils/hooks/hooksConfigSnapshot.ts', ['getHooksFromAllowedSources', 'shouldAllowManagedHooksOnly', 'shouldDisableAllHooksIncludingManaged'])
  const matching = await extract('../../../src/utils/hooks.ts', ['matchesPattern', 'hookDedupKey', 'getHooksConfig', 'getMatchingHooks'])
  const { normalizeLegacyToolName, getLegacyToolNames } = await import('../../../src/utils/permissions/permissionRuleParser')
  const rows = []
  for (const profile of input.selection) {
    const policy = profile.policy ?? null
    const settingsModule = { getSettingsForSource: () => policy, getSettings_DEPRECATED: () => profile.settings }
    const isRestrictedToPluginOnly = () => policy?.strictPluginOnlyCustomization === true || policy?.strictPluginOnlyCustomization?.includes?.('hooks') === true
    const production = new Function('settingsModule', 'isRestrictedToPluginOnly', 'getRegisteredHooks', 'getSessionHooks', 'getSessionFunctionHooks', 'normalizeLegacyToolName', 'getLegacyToolNames', 'logForDebugging', 'prepareIfConditionMatcher', 'DEFAULT_HOOK_SHELL', `${snapshot}\nconst getHooksConfigFromSnapshot = () => getHooksFromAllowedSources();\n${matching}\nreturn {getHooksFromAllowedSources,shouldAllowManagedHooksOnly,shouldDisableAllHooksIncludingManaged,getMatchingHooks}`)(settingsModule, isRestrictedToPluginOnly, () => ({}), (_state: any, _sid: any, event: any) => new Map([[event, profile.session ?? []]]), () => new Map(), normalizeLegacyToolName, getLegacyToolNames, () => {}, async () => undefined, 'bash')
    const config = production.getHooksFromAllowedSources()
    const disabled = production.shouldDisableAllHooksIncludingManaged()
    const matched = disabled ? [] : await production.getMatchingHooks({}, 'actual-agent', profile.event, {hook_event_name:profile.event,agent_type:profile.agentType})
    rows.push({config, managedOnly:production.shouldAllowManagedHooksOnly(),disabled,hooks:matched.map((row:any)=>row.hook)})
  }
  process.stdout.write(JSON.stringify({rows}))
}
else if (input.schema) process.stdout.write(JSON.stringify(recipe(hookJSONOutputSchema())))
else if (input.events) {
  const { parse } = await import('acorn')
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/cli/print.ts', import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  let callback = ''
  const visit = (node: any) => {
    if (!node || typeof node !== 'object') return
    if (node.type === 'CallExpression' && node.callee?.name === 'registerHookEventHandler' && node.arguments[0]?.type === 'ArrowFunctionExpression') callback = source.slice(node.arguments[0].start, node.arguments[0].end)
    for (const value of Object.values(node)) if (Array.isArray(value)) value.forEach(visit); else if (value && typeof value === 'object') visit(value)
  }
  visit(ast)
  if (!callback) throw new Error('Actual print Hook event handler unavailable')
  const hooks = await import('../../../src/utils/hooks/hookEvents')
  const rows = []
  for (const profile of input.events) {
    hooks.clearHookEventState()
    hooks.setAllHookEventsEnabled(profile.all)
    const output: unknown[] = []
    const handler = new Function('randomUUID', 'getSessionId', 'structuredIO', `return ${callback}`)(() => 'source-event-uuid', () => 'source-session-id', { write: (message: unknown) => { output.push(message); return Promise.resolve() } })
    if (profile.handler) hooks.registerHookEventHandler(handler)
    hooks.emitHookStarted('actual-hook-id', 'SubagentStop', 'SubagentStop')
    hooks.emitHookProgress({ hookId: 'actual-hook-id', hookName: 'SubagentStop', hookEvent: 'SubagentStop', stdout: 'actual stdout', stderr: 'actual stderr', output: 'actual stdoutactual stderr' })
    hooks.emitHookResponse({ hookId: 'actual-hook-id', hookName: 'SubagentStop', hookEvent: 'SubagentStop', stdout: 'actual stdout', stderr: 'actual stderr', output: 'actual stdoutactual stderr', exitCode: 2, outcome: 'error' })
    rows.push(output)
  }
  process.stdout.write(JSON.stringify({ rows }))
}
else if (input.pipeline || input.hint) {
  const { parse } = await import('acorn')
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/hooks.ts', import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const names = new Set(['validateHookJson', 'parseHookOutput', 'processHookJSONOutput'])
  const bodies: string[] = []
  for (const node of ast.body) if (node.type === 'FunctionDeclaration' && names.has(node.id.name)) bodies.push(source.slice(node.start, node.end))
  if (bodies.length !== 3) throw new Error('Actual original hook output functions unavailable')
  // Only the allocation/logging/JSON primitives are injected. Selection and
  // schema/output business run the original production function bodies.
  const production = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', 'createAttachmentMessage', `${bodies.join('\n')}\nreturn {parseHookOutput,processHookJSONOutput}`)(hookJSONOutputSchema, JSON.parse, JSON.stringify, () => {}, (attachment: any) => ({ attachment }))
  if (input.hint) {
    const result = production.parseHookOutput('{"continue":"invalid"}')
    process.stdout.write(JSON.stringify(result.validationError.split('\n\nExpected schema:\n')[1]))
  } else process.stdout.write(JSON.stringify({ rows: input.pipeline.map((row: any) => {
    const parsed = production.parseHookOutput(row.stdout)
    let processed, failure = null
    if (parsed.json && !parsed.json.async) try { processed = production.processHookJSONOutput({ json: parsed.json, command: row.command, hookName: row.event, toolUseID: 'original-output-id', hookEvent: row.event, expectedHookEvent: row.event, stdout: row.stdout, stderr: row.stderr, exitCode: row.code, durationMs: 42 }) } catch (error) { failure = (error as Error).message }
    return { parsed, processed, failure }
  }) }))
} else {
  const rows = input.values.map((value: unknown) => {
    const result = hookJSONOutputSchema().safeParse(value)
    return result.success ? { value: result.data } : { issues: result.error.issues.map((issue: any) => ({ path: issue.path, message: issue.message })) }
  })
  process.stdout.write(JSON.stringify({ rows }))
}
