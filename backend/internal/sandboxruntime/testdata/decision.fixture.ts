import fs from 'node:fs'
import {parse} from 'acorn'
import {splitCommand_DEPRECATED} from '../../../../src/utils/bash/commands'
import {bashPermissionRule,matchWildcardPattern,stripAllLeadingEnvVars,stripSafeWrappers,BINARY_HIJACK_VARS} from '../../../../src/tools/BashTool/bashPermissions'
const input=JSON.parse(await Bun.stdin.text())
const source=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/tools/BashTool/shouldUseSandbox.ts',import.meta.url),'utf8'))
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const compiled=nodes.filter((n:any)=>n?.type==='FunctionDeclaration').map((n:any)=>source.slice(n.start,n.end)).join('\n')
process.env.USER_TYPE='external'
const output=[]
for(const row of input.rows){
 const SandboxManager={isSandboxingEnabled:()=>row.enabled,areUnsandboxedCommandsAllowed:()=>row.settings.sandbox?.allowUnsandboxedCommands??true}
 const exports={}
 const fn=new Function('exports','SandboxManager','getSettings_DEPRECATED','splitCommand_DEPRECATED','bashPermissionRule','matchWildcardPattern','stripAllLeadingEnvVars','stripSafeWrappers','BINARY_HIJACK_VARS','getFeatureValue_CACHED_MAY_BE_STALE',compiled+';return shouldUseSandbox')
 const invoke=fn(exports,SandboxManager,()=>row.settings,splitCommand_DEPRECATED,bashPermissionRule,matchWildcardPattern,stripAllLeadingEnvVars,stripSafeWrappers,BINARY_HIJACK_VARS,()=>({commands:[],substrings:[]}))
 output.push(invoke(row.input))
}
process.stdout.write(JSON.stringify(output)+'\n')
