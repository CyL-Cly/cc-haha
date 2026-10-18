import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original pane-team footer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_TEAM_FOOTER_SOURCE !== '1') throw new Error('Missing original pane-team footer guard')
const ts = createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
function declarations(relative: string,names: string[]) {
  const file = new URL('../../../src/'+relative,import.meta.url).pathname
  const source = ts.createSourceFile(file,fs.readFileSync(file,'utf8'),ts.ScriptTarget.Latest,true,relative.endsWith('tsx')?ts.ScriptKind.TSX:ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any)=>ts.isFunctionDeclaration(node)&&names.includes(node.name?.text))
  if (nodes.length!==names.length) throw new Error('Original functions changed: '+names)
  return ts.transpileModule(nodes.map((node: any)=>node.getText(source).replace(/^export /,'')).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
}
const modeJS = declarations('utils/swarm/backends/registry.ts',['isInProcessEnabled'])
const arrayJS = declarations('utils/array.ts',['count'])
const sourceFile = new URL('../../../src/components/PromptInput/PromptInputFooterLeftSide.tsx',import.meta.url).pathname
const source = ts.createSourceFile(sourceFile,fs.readFileSync(sourceFile,'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
const wanted = new Set(['hasTeams','primaryItemCount','shouldShowPrStatus'])
const expressions: Record<string,string>={}
const scan = (node: any)=>{if(ts.isVariableDeclaration(node)&&wanted.has(node.name?.text))expressions[node.name.text]=node.initializer.getText(source);ts.forEachChild(node,scan)}
scan(source)
if(Object.keys(expressions).length!==3)throw new Error('Original pane-team/PR callers changed')
const footerJS=ts.transpileModule(arrayJS+'\nconst hasTeams='+expressions.hasTeams+';const primaryItemCount='+expressions.primaryItemCount+';const shouldShowPrStatus='+expressions.shouldShowPrStatus+';return {hasTeams,primaryItemCount,shouldShowPrStatus}',{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const modes=input.modes.map((row: any)=>{
  const dependencies={getIsNonInteractiveSession:()=>row.nonInteractive,getTeammateMode:()=>row.mode,inProcessFallbackActive:row.fallback,isInsideTmuxSync:()=>row.tmux,isInITerm2:()=>row.iterm,logForDebugging:()=>{}}
  return new Function(...Object.keys(dependencies),modeJS+';return isInProcessEnabled()')(...Object.values(dependencies))
})
const footers=input.footers.map((row: any)=>new Function('isAgentSwarmsEnabled','isInProcessEnabled','teamContext','isCoordinator','hasActiveMode','hasBackgroundTasks','isPrStatusEnabled','prStatus','columns',footerJS)(()=>row.teams,()=>row.inProcess,row.context,!!row.coordinator,!!row.activeMode,!!row.background,()=>row.prEnabled,row.pr,row.columns))
const promptFile = new URL('../../../src/components/PromptInput/PromptInput.tsx',import.meta.url).pathname
const promptSource=ts.createSourceFile(promptFile,fs.readFileSync(promptFile,'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
let cachedTeamsExpression:string|undefined
function findCachedTeams(node:any){if(ts.isVariableDeclaration(node)&&node.name.getText(promptSource)==='cachedTeams')cachedTeamsExpression=node.initializer.getText(promptSource);ts.forEachChild(node,findCachedTeams)}
findCachedTeams(promptSource)
if(!cachedTeamsExpression)throw new Error('Original cachedTeams getter missing')
const cachedTeamsJS=ts.transpileModule(arrayJS+'\nreturn '+cachedTeamsExpression,{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const cachedTeams=(input.cachedTeams??[]).map((row:any)=>new Function('useMemo','isAgentSwarmsEnabled','isInProcessEnabled','teamContext',cachedTeamsJS)((fn:any)=>fn(),()=>row.teams,()=>row.inProcess,row.context))
const reconnectJS=declarations('utils/swarm/reconnection.ts',['computeInitialTeamContext'])
const children=(input.children??[]).map((row: any)=>{
  const file=input.home+'/child-'+row.name+'.json'
  fs.writeFileSync(file,JSON.stringify(row.file))
  const context=new Function('getDynamicTeamContext','readTeamFile','getTeamFilePath','logForDebugging','logError',reconnectJS+';return computeInitialTeamContext()')(()=>row.role,()=>JSON.parse(fs.readFileSync(file,'utf8')),()=>file,()=>{},()=>{})
  return {context,hasTeams:context?new Function('isAgentSwarmsEnabled','isInProcessEnabled','teamContext',arrayJS+';return '+expressions.hasTeams)(()=>true,()=>false,context):false}
})
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({modes,footers,children,cachedTeams}),error=>error?reject(error):resolve()))
