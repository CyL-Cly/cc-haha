import fs from 'node:fs'
import {parse} from 'acorn'
const input=JSON.parse(await Bun.stdin.text())
const text=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/sandbox/sandbox-adapter.ts',import.meta.url),'utf8'))
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing Source '+name);return text.slice(n.start,n.end)}
const output=[]
for(const row of input.rows){
 let initializeCalls=0,wrapCalls=0,subscriptionCalls=0,detectCalls=0,converts=0,settings={sandbox:{enabled:true}},failure=row.failure
 const logs:string[]=[]
 const deps={isSandboxingEnabled:()=>true,getCwdState:()=>'/private-source-cwd',detectWorktreeMainRepoPath:async()=>{detectCalls++;return null},getSettings_DEPRECATED:()=>settings,convertToSandboxRuntimeConfig:()=>{converts++;if(failure==='convert')throw new Error('Path contains null bytes');return{}},settingsChangeDetector:{subscribe:()=>{subscriptionCalls++;return()=>{}}},BaseSandboxManager:{initialize:async()=>{initializeCalls++;if(failure==='manager')throw new Error('actual primitive failed')},wrapWithSandbox:async(command:string)=>{wrapCalls++;return command}},logForDebugging:(s:string)=>logs.push(s),errorMessage:(e:any)=>e.message,shouldAllowManagedSandboxDomainsOnly:()=>false}
 const api=new Function('dependencies',`with(dependencies){let initializationPromise,settingsSubscriptionCleanup,worktreeMainRepoPath;${['initialize','wrapWithSandbox'].map(extract).join('\n')};return {initialize,wrapWithSandbox}}`)(deps)
 await api.initialize()
 let wrapError=null
 try{await api.wrapWithSandbox('first')}catch(e:any){wrapError=e.message}
 const before={initializeCalls,wrapCalls,subscriptionCalls,detectCalls,converts,logs:[...logs],wrapError}
 failure=null
 await api.initialize()
 const wrapped=await api.wrapWithSandbox('second')
 await api.initialize()
 output.push({before,after:{initializeCalls,wrapCalls,subscriptionCalls,detectCalls,converts,logs:[...logs],wrapped}})
}
process.stdout.write(JSON.stringify(output)+'\n')
