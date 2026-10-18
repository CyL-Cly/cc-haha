import fs from 'node:fs'
import {join} from 'node:path'
import {PassThrough} from 'node:stream'
import {mock} from 'bun:test'
import {createRequire} from 'node:module'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original whole TeamsDialog')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_TEAMS_SOURCE!=='1')throw new Error('Missing original TeamsDialog guard')
// Source declaration tooling is prepared before the business FS guard. Its
// immutable dependency startup probes the host filesystem's case sensitivity.
const declarationTS=input.capturedFooterCaller?createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript'):undefined
const configDir=join(home,'.claude')
const bin=join(home,'bin'),receipt=join(home,'pane-requests.jsonl')
fs.mkdirSync(configDir,{recursive:true});fs.mkdirSync(bin,{recursive:true})
fs.writeFileSync(join(configDir,'settings.json'),'{}')
fs.writeFileSync(join(configDir,'.claude.json'),JSON.stringify({hasCompletedOnboarding:true,projects:{[home]:{hasTrustDialogAccepted:true}}}))
for(const command of ['tmux','it2'])fs.writeFileSync(join(bin,command),'#!/usr/bin/python3\nimport os,sys,json\nwith open(os.environ["CC_HAHA_TEAM_PANE_RECEIPT"],"a") as f:f.write(json.dumps({"pid":os.getpid(),"command":os.path.basename(sys.argv[0]),"args":sys.argv[1:]})+"\\n")\nif "kill-pane" in sys.argv and os.environ.get("CC_HAHA_TEAM_HELD_PANE") in sys.argv:\n with open(os.environ["CC_HAHA_TEAM_PANE_RELEASE"]) as f:f.read()\nprint("tmux 3.5")\nsys.exit(1 if "kill-pane" in sys.argv and os.environ.get("CC_HAHA_TEAM_FAIL_KILL")=="1" else 0)\n',{mode:0o700})
process.env.PATH=bin+':/usr/bin:/bin'
process.env.CC_HAHA_TEAM_PANE_RECEIPT=receipt
if(input.failKill)process.env.CC_HAHA_TEAM_FAIL_KILL='1'
if(input.heldPane){process.env.CC_HAHA_TEAM_HELD_PANE=input.heldPane;process.env.CC_HAHA_TEAM_PANE_RELEASE=join(home,'pane-release')}
if(input.insideTmux)process.env.TMUX=join(home,'private-tmux-socket')+',1,0'
else delete process.env.TMUX
if(input.backend==='iterm2')process.env.TERM_PROGRAM='iTerm.app'
const ownedCommands=new Set(['tmux','it2',join(bin,'tmux'),join(bin,'it2')])
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths(),privateExecutableFiles:[join(bin,'tmux'),join(bin,'it2')],approvedSpawn:({command,args})=>ownedCommands.has(command)&&args.every(arg=>typeof arg==='string')&&(args[0]==='-V'||args.includes('select-pane')||args.includes('kill-pane')||args[0]==='session'&&['list','focus','close'].includes(args[1]!))})
const {plainTextStorage}=await import('../../../src/utils/secureStorage/plainTextStorage')
const {createFallbackStorage}=await import('../../../src/utils/secureStorage/fallbackStorage')
const privateStorage=createFallbackStorage({name:'owned-unavailable-keychain',read:()=>null,readAsync:async()=>null,update:()=>({success:false}),delete:()=>false},plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts',()=>({getSecureStorage:()=>privateStorage}))
;(globalThis as any).MACRO={VERSION:'999.0.0-teams-source',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home);bootstrap.setOriginalCwd(home);bootstrap.setProjectRoot(home);bootstrap.setAllowedSettingSources(['userSettings']);bootstrap.setIsInteractive(true)
const config=await import('../../../src/utils/config');config.enableConfigs()
const helpers=await import('../../../src/utils/swarm/teamHelpers')
await helpers.writeTeamFileAsync(input.team,input.file)
const tasks=await import('../../../src/utils/tasks')
for(const task of input.tasks??[])await tasks.createTask(input.team,task)
const registry=await import('../../../src/utils/swarm/backends/registry')
if(input.detect!==false)await registry.detectAndGetBackend()
const discovery=await import('../../../src/utils/teamDiscovery')
const mailbox=await import('../../../src/utils/teammateMailbox')
const React=await import('react')
const {render}=await import('../../../src/ink')
const {AppStoreContext,getDefaultAppState,useAppState}=await import('../../../src/state/AppState')
const {createStore}=await import('../../../src/state/store')
const {KeybindingProvider}=await import('../../../src/keybindings/KeybindingContext')
const {parseBindings}=await import('../../../src/keybindings/parser')
const {DEFAULT_BINDINGS}=await import('../../../src/keybindings/defaultBindings')
const {TeamsDialog}=await import('../../../src/components/teams/TeamsDialog')
const {ThemeProvider}=await import('../../../src/components/design-system/ThemeProvider')
const {Parser}=await import('../../../src/ink/termio')
const instances=(await import('../../../src/ink/instances')).default
const paintCells=(value:string)=>new Parser().feed(value).flatMap(action=>action.type==='text'?action.graphemes.map(grapheme=>({text:grapheme.value,style:action.style})):[])
const initial=getDefaultAppState()
initial.toolPermissionContext={...initial.toolPermissionContext,isBypassPermissionsModeAvailable:!!input.bypass}
initial.teamContext={teamName:input.team,leadAgentId:'lead@'+input.team,teammates:Object.fromEntries(input.file.members.map((member:any)=>[member.agentId,member]))}
const store=createStore(initial)
const contexts=new Set(['Confirmation'])
const provider={bindings:parseBindings(DEFAULT_BINDINGS),handlerRegistryRef:{current:new Map()},pendingChordRef:{current:null},pendingChord:null,setPendingChord:()=>{},activeContexts:contexts,registerActiveContext:(value:any)=>contexts.add(value),unregisterActiveContext:(value:any)=>contexts.delete(value)}
const stdin=new PassThrough() as any;stdin.isTTY=true;stdin.setRawMode=()=>{};stdin.ref=()=>stdin;stdin.unref=()=>stdin
const stdout=new PassThrough() as any;stdout.columns=input.width??120;stdout.isTTY=false
let output='',done=0;stdout.on('data',(chunk:any)=>output+=chunk.toString())
let app:any
const onDone=()=>{done++;app.rerender(React.createElement(AppStoreContext.Provider,{value:store},null))}
let originalCachedTeams:any,isTeamsEnabled:any,count:any
if(input.capturedFooterCaller){const ts=declarationTS;const source=ts.createSourceFile('PromptInput.tsx',fs.readFileSync(new URL('../../../src/components/PromptInput/PromptInput.tsx',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);let expression:string|undefined;const visit=(node:any)=>{if(ts.isVariableDeclaration(node)&&node.name.getText(source)==='cachedTeams')expression=node.initializer.getText(source);ts.forEachChild(node,visit)};visit(source);if(!expression)throw new Error('Original cached Footer caller missing');const js=ts.transpileModule('return '+expression,{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText;originalCachedTeams=new Function('useMemo','isAgentSwarmsEnabled','isInProcessEnabled','count','teamContext',js);isTeamsEnabled=(await import('../../../src/utils/agentSwarmsEnabled')).isAgentSwarmsEnabled;count=(await import('../../../src/utils/array')).count}
function OriginalFooterCaller(){const teamContext=useAppState((state:any)=>state.teamContext);const teams=originalCachedTeams(React.useMemo,isTeamsEnabled,registry.isInProcessEnabled,count,teamContext);return React.createElement(TeamsDialog,{initialTeams:teams,onDone})}
const view=(initialTeams:any[])=>React.createElement(AppStoreContext.Provider,{value:store},React.createElement(ThemeProvider,{initialState:input.theme??'dark'},React.createElement(KeybindingProvider,provider as any,input.capturedFooterCaller?React.createElement(OriginalFooterCaller):React.createElement(TeamsDialog,{initialTeams,onDone}))))
app=await render(view([{name:input.team,memberCount:input.file.members.length-1,runningCount:0,idleCount:0}]),{stdin,stdout,stderr:stdout,exitOnCtrlC:false,patchConsole:false})
const deadline=Date.now()+12000
const flush=async()=>{for(let i=0;i<8;i++)await new Promise(resolve=>setImmediate(resolve))}
const until=async(predicate:()=>boolean|Promise<boolean>,label:string)=>{while(!(await predicate())){if(Date.now()>deadline)throw new Error(label+': '+JSON.stringify({blocked:caps.blocked,output}));await new Promise(resolve=>setTimeout(resolve,5))};await flush()}
const frames=[]
const requests=()=>{try{return fs.readFileSync(receipt,'utf8').trim().split('\n').filter(Boolean).map(line=>JSON.parse(line))}catch{return []}}
let paintStage=0
const paintProjection=()=>{const frame=output.split('\x1b[?2026h').at(-1)?.replace(/\x1b\[\?2026l$/,'')??'';const visit=(node:any):any=>({name:node.nodeName,text:node.nodeValue,style:node.style,box:node.yogaNode?{width:node.yogaNode.getComputedWidth(),height:node.yogaNode.getComputedHeight(),left:node.yogaNode.getComputedLeft(),top:node.yogaNode.getComputedTop()}:undefined,children:node.childNodes?.map(visit)});return input.nativeRows?{cells:paintCells(frame),nativeCells:paintCells((input.nativeRows[paintStage++]??[]).join('\n')),layout:input.captureLayout?visit(instances.get(stdout)!.rootNode):undefined}:{}}
const snapshot=async()=>({...paintProjection(),statuses:discovery.getTeammateStatuses(input.team),file:helpers.readTeamFile(input.team),tasks:await tasks.listTasks(input.team),inboxes:Object.fromEntries(await Promise.all(input.file.members.filter((member:any)=>member.name!=='team-lead').map(async(member:any)=>[member.name,await mailbox.readMailbox(member.name,input.team)]))),roster:store.getState().teamContext?.teammates,inbox:store.getState().inbox.messages,requests:requests(),done,output,frame:output.split('\x1b[?2026h').at(-1)?.replace(/\x1b\[\?2026l$/,'')})
try{
 await until(()=>output.includes('Team '+input.team),'Original TeamsDialog mounted')
 frames.push({stage:'initial',...await snapshot()})
 for(const step of input.steps??[]){
  if(step.replaceFile)await helpers.writeTeamFileAsync(input.team,step.replaceFile)
  if(step.deleteTeam){output='';const{TeamDeleteTool}=await import('../../../src/tools/TeamDeleteTool/TeamDeleteTool');const result=await TeamDeleteTool.call({},{getAppState:store.getState,setAppState:store.setState} as any);if(!result.data.success||store.getState().teamContext)throw new Error('Original actual TeamDelete failed');await until(()=>output.includes('Team '+input.team)&&output.includes('No teammates'),'Original mounted dialog retained captured initial team after actual TeamDelete')}
  if(step.key){output='';stdin.write(step.key);await flush();if(input.nativeRows)await until(()=>output.includes('\x1b[?2026l'),'Original paint committed frame')}
  if(step.release)fs.writeFileSync(join(home,'pane-release'),'release')
  if(step.text)await until(()=>output.includes(step.text),'Original TeamsDialog text '+step.text)
  if(step.mode)await until(()=>helpers.readTeamFile(input.team)?.members.some((member:any)=>member.name===step.member&&member.mode===step.mode),'Original mode config commit')
  if(step.removed)await until(()=>!helpers.readTeamFile(input.team)?.members.some((member:any)=>member.name===step.removed),'Original pane member removal')
  if(step.mailbox)await until(async()=>(await mailbox.readMailbox(step.member,input.team)).filter((row:any)=>{try{return JSON.parse(row.text).type===step.mailbox}catch{return false}}).length>=step.count,'Original mailbox '+step.mailbox)
  if(step.mailboxes)for(const target of step.mailboxes)await until(async()=>(await mailbox.readMailbox(target.member,input.team)).filter((row:any)=>{try{return JSON.parse(row.text).type===target.type}catch{return false}}).length>=target.count,'Original completed mailbox '+target.member)
  if(step.requests)await until(()=>requests().length>=step.requests,'Original native pane process')
  if(step.done)await until(()=>done>=step.done,'Original Dialog completion')
  if(step.noticeCount)await until(()=>store.getState().inbox.messages.length>=step.noticeCount,'Original owned task reclamation notice')
  if(step.gonePane)await until(()=>{try{const row=requests().find((row:any)=>row.args.includes(step.gonePane));process.kill(row.pid,0);return false}catch(error:any){return error.code==='ESRCH'}},'Original native pane child reaped')
  if(step.setActive!==undefined){await helpers.setMemberActive(input.team,step.member,step.setActive);await until(()=>output.includes('[idle]'),'Original1s refresh')}
  frames.push({stage:step.stage,...await snapshot()})
 }
 const exited=app.waitUntilExit();app.unmount();await exited
 if(caps.blocked.length)throw new Error('Original TeamsDialog boundary '+JSON.stringify(caps.blocked))
 await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({frames,blocked:caps.blocked,pid:process.pid}),error=>error?reject(error):resolve()))
}finally{app.unmount()}
process.exit(0)
