import fs from 'node:fs'
import path from 'node:path'
import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text()),home=assertDisposableFixtureHome(input.home,'actual Sandbox adapter config')
if(Bun.version!=='1.3.14')throw new Error('pinned original runtime required')
const {expandPath}=await import('../../../../src/utils/path')
const text=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/sandbox/sandbox-adapter.ts',import.meta.url),'utf8'))
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing Source '+name);return text.slice(n.start,n.end)}
const results=[]
for(const row of input.rows){
 const sources=new Map(row.sources.map((s:any)=>[s.name,s]))
 const scrub:string[]=[]
 const deps:any={pathExists:fs.existsSync,getSettingsForSource:(source:string)=>sources.get(source)?.settings,resolve:path.resolve,sep:path.sep,getCwdState:()=>row.cwd,getOriginalCwd:()=>row.originalCwd,getSettingsRootPathForSource:(source:string)=>sources.get(source)?.root,getSettingsFilePathForSource:(source:string)=>sources.get(source)?.file,getManagedSettingsDropInDir:()=>row.managedDropInDirectory,getClaudeTempDir:()=>row.temporaryDirectory,getAdditionalDirectoriesForClaudeMd:()=>row.additionalDirectories??[],ripgrepCommand:()=>({rgPath:'rg',rgArgs:['--fixture']}),expandPath,statSync:fs.statSync,SETTING_SOURCES:['userSettings','projectSettings','localSettings','flagSettings','policySettings'],FILE_EDIT_TOOL_NAME:'Edit',FILE_READ_TOOL_NAME:'Read',WEB_FETCH_TOOL_NAME:'WebFetch',bareGitRepoScrubPaths:scrub,worktreeMainRepoPath:row.mainWorktreeRepository??null}
 const fn=new Function('dependencies',`with(dependencies){${['permissionRuleValueFromString','resolvePathPatternForSandbox','resolveSandboxFilesystemPath','shouldAllowManagedSandboxDomainsOnly','shouldAllowManagedReadPathsOnly','convertToSandboxRuntimeConfig'].map(extract).join('\n')};return convertToSandboxRuntimeConfig}`)(deps)
 let error:string|null=null,config:any=null
 try {config=fn(row.settings)}catch(e:any){error=e.message}
 results.push({config,scrubPaths:scrub,error})
}
process.stdout.write(JSON.stringify(results)+'\n')
