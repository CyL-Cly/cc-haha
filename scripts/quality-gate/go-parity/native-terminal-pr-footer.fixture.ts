import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original PR footer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PR_SOURCE !== '1') throw new Error('Missing original PR guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = (path: string) => ts.createSourceFile(path, fs.readFileSync(new URL('../../../src/'+path,import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,path.endsWith('tsx')?ts.ScriptKind.TSX:ts.ScriptKind.TS)
const gh = source('utils/ghPrStatus.ts')
const functions = ['deriveReviewState','fetchPrStatus'].map(name => {
  const node = gh.statements.find((node: any)=>ts.isFunctionDeclaration(node)&&node.name?.text===name)
  if (!node) throw new Error('Missing original '+name)
  return node.getText(gh).replace(/^export /,'')
})
const timeout = gh.statements.find((node: any)=>ts.isVariableStatement(node)&&node.declarationList.declarations.some((d: any)=>d.name.text==='GH_TIMEOUT_MS'))
if (!timeout) throw new Error('Missing original gh timeout')
const body = ts.transpileModule(timeout.getText(gh)+'\n'+functions.join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText
const fetchResults = []
for (const row of input.fetch ?? []) {
  const calls: any[] = []
  const deps = {
    getIsGit:async()=>row.isGit, getBranch:async()=>row.branch, getDefaultBranch:async()=>row.defaultBranch,
    execFileNoThrow:async (...args: any[])=>{calls.push(args);return {code:row.code??0,stdout:row.stdout??''}},jsonParse:JSON.parse,
  }
  const fetch = new Function(...Object.keys(deps),body+'\nreturn fetchPrStatus')(...Object.values(deps))
  fetchResults.push({value:await fetch(),calls})
}
const footer = source('components/PromptInput/PromptInputFooterLeftSide.tsx')
let visible: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node)&&node.name.getText(footer)==='shouldShowPrStatus') visible=node.initializer
  ts.forEachChild(node,visit)
}
visit(footer)
if (!visible) throw new Error('Missing actual PR visibility caller')
const visibleJS = ts.transpileModule('const visible = '+visible.getText(footer),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const enabledNode=footer.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='isPrStatusEnabled')
if(!enabledNode)throw new Error('Missing actual original PR global getter')
const enabledJS=ts.transpileModule(enabledNode.getText(footer),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const settingEnabled=(input.globalSettings??[]).map((row:any)=>Boolean(new Function('getGlobalConfig',enabledJS+';return isPrStatusEnabled()')(()=>row)))
const visibility = (input.visibility??[]).map((row: any)=>new Function('isPrStatusEnabled','prStatus','primaryItemCount','columns',visibleJS+';return visible')(()=>row.enabled,row.status,row.primary,row.columns))
const badgeSource=source('components/PrBadge.tsx')
const badgeNodes=['PrBadge','getPrStatusColor'].map(name=>{
  const node=badgeSource.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text===name)
  if(!node)throw new Error('Missing actual original PR badge '+name)
  return node.getText(badgeSource).replace(/^export /,'')
})
const badgeJS=ts.transpileModule(badgeNodes.join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext,jsx:ts.JsxEmit.React}}).outputText
const createElement=(type:any,props:any,...children:any[])=>({type,props:props??{},children})
const PrBadge=new Function('_c','React','Link','Text',badgeJS+';return PrBadge')((n:number)=>Array(n).fill(Symbol.for('react.memo_cache_sentinel')),{createElement},'Link','Text')
const badges=(input.badges??[]).map((row:any)=>PrBadge(row))
const themeSource=source('utils/theme.ts')
const themeNames=['lightTheme','lightAnsiTheme','darkAnsiTheme','lightDaltonizedTheme','darkTheme','darkDaltonizedTheme']
const themeNodes=themeSource.statements.filter((n:any)=>ts.isVariableStatement(n)&&n.declarationList.declarations.some((d:any)=>themeNames.includes(d.name.text)))
const getThemeNode=themeSource.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='getTheme')
if(themeNodes.length!==6||!getThemeNode)throw new Error('Original themes missing')
const themeJS=ts.transpileModule(themeNodes.map((n:any)=>n.getText(themeSource)).join('\n')+'\n'+getThemeNode.getText(themeSource).replace(/^export /,''),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const getTheme=new Function(themeJS+';return getTheme')()
const themeRoles=Object.fromEntries(['light','light-ansi','dark-ansi','light-daltonized','dark','dark-daltonized'].map(name=>[name,Object.fromEntries(['success','error','warning','merged'].map(role=>[role,getTheme(name)[role]]))]))

// Execute the original hook, including its async closures and cleanup, with
// external React/clock/timer primitives. No rewritten polling policy is used.
const hook = source('hooks/usePrStatus.ts')
const hookBody = hook.statements.filter((node: any)=>!ts.isImportDeclaration(node)&&!ts.isTypeAliasDeclaration(node)).map((node: any)=>node.getText(hook).replace(/^export /,'')).join('\n')
const hookJS = ts.transpileModule(hookBody,{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const polling = []
for (const row of input.polling??[]) {
  let clock=row.start, interaction=0, loading=false, enabled=true, state: any, effect: any, cleanup: any, oldDeps: any[]|undefined
  let cursor=0, nextTimer=0
  const refs: any[]=[]
  const timers=new Map<number,{at:number,fn:()=>void}>()
  const pending: {started:number,settle:(value:any)=>void}[]=[]
  const DatePrimitive={now:()=>clock}
  const deps={
    Date:DatePrimitive,getLastInteractionTime:()=>interaction,
    useState:(initial:any)=>{state??=structuredClone(initial);return [state,(update:any)=>{state=update(state)}]},
    useRef:(initial:any)=>{const index=cursor++;return refs[index]??=( {current:initial} )},
    useEffect:(run:any,newDeps:any[])=>{if(!oldDeps||newDeps.some((v:any,i:number)=>v!==oldDeps![i])){cleanup?.();oldDeps=[...newDeps];effect=run}},
    setTimeout:(fn:()=>void,delay:number)=>{const id=++nextTimer;timers.set(id,{at:clock+delay,fn});return id},
    clearTimeout:(id:number)=>timers.delete(id),
    fetchPrStatus:()=>new Promise(resolve=>pending.push({started:clock,settle:resolve})),
  }
  const hookFunction=new Function(...Object.keys(deps),hookJS+';return usePrStatus')(...Object.values(deps))
  const render=()=>{cursor=0;hookFunction(loading,enabled);if(effect){const run=effect;effect=undefined;cleanup=run()}}
  render()
  const trace=[]
  const flush=async()=>{await Promise.resolve();await Promise.resolve()}
  for(const step of row.steps){
    clock=step.at
    if(step.op==='resolve')pending[step.index??pending.length-1]?.settle(step.value??null)
    else if(step.op==='loading'){loading=step.value;render()}
    else if(step.op==='enabled'){enabled=step.value;render()}
    else if(step.op==='interaction')interaction++
    else if(step.op==='tick'){
      for(const [id,timer] of [...timers])if(timer.at<=clock){timers.delete(id);timer.fn()}
    }else if(step.op==='unmount')cleanup?.()
    else throw new Error('Unknown original hook step')
    await flush()
    trace.push({state:structuredClone(state),fetches:pending.map(p=>p.started),timers:[...timers.values()].map(t=>t.at).sort((a,b)=>a-b),disabled:refs[1]?.current})
  }
  cleanup?.()
  polling.push(trace)
}
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({fetchResults,visibility,badges,themeRoles,settingEnabled,polling}),error=>error?reject(error):resolve()))
