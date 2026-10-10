import fs from 'node:fs'
import {parse} from 'acorn'
import {extractOutputRedirections,splitCommand_DEPRECATED} from '../../../../src/utils/bash/commands'
import {bashPermissionRule,matchWildcardPattern,stripAllLeadingEnvVars,stripSafeWrappers} from '../../../../src/tools/BashTool/bashPermissions'
import {getRuleByContentsForToolName,createPermissionRequestMessage} from '../../../../src/utils/permissions/permissions'
const input=JSON.parse(await Bun.stdin.text())
const text=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/tools/BashTool/bashPermissions.ts',import.meta.url),'utf8'))
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing actual Source '+name);return text.slice(n.start,n.end)}
const dependencies={BashTool:{name:'Bash'},getRuleByContentsForTool:(context:any,_tool:any,behavior:any)=>getRuleByContentsForToolName(context,'Bash',behavior),extractOutputRedirections,splitCommand:splitCommand_DEPRECATED,bashPermissionRule,matchWildcardPattern,stripAllLeadingEnvVars,stripSafeWrappers,createPermissionRequestMessage}
const original=new Function('dependencies',`with(dependencies){${['filterRulesByContentsMatchingInput','matchingRulesForInput','checkSandboxAutoAllow'].map(extract).join('\n')};return checkSandboxAutoAllow}`)(dependencies)
process.stdout.write(JSON.stringify(input.rows.map((row:any)=>original(row.input,row.context)))+'\n')
