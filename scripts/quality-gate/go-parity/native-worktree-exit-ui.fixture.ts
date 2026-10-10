import { dirname,join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Worktree exit UI contract')
if(process.env.CC_HAHA_WORKTREE_EXIT_UI_FIXTURE!=='1')throw new Error('Missing Worktree fixture')
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(input.homeDir);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false);state.setInitialMainLoopModel('deepseek-flash')
const config=await import('../../../src/utils/config');config.enableConfigs()
const {mock}=await import('bun:test')
const React=await import('react')
let slots:any[]=[]
let index=0
mock.module('react',()=>({...React,default:React.default,useEffect:()=>{},useState:()=>[slots[index++],()=>{}]}))
const worktree=await import('../../../src/utils/worktree')
const {WorktreeExitDialog}=await import('../../../src/components/WorktreeExitDialog')
const rows=[]
for(const row of input.rows){
 worktree.restoreWorktreeSession({originalCwd:input.homeDir,worktreePath:row.path,worktreeName:'fixture',worktreeBranch:row.branch,sessionId:'fixture',tmuxSessionName:row.tmux||undefined} as any)
 slots=['asking',row.files,row.commits,undefined];index=0
 const view:any=WorktreeExitDialog({onDone:()=>{},onCancel:()=>{}})
 rows.push({subtitle:view.props.subtitle,options:view.props.children.props.options,defaultValue:view.props.children.props.defaultFocusValue})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
process.exit(0)
