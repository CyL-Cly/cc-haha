import fs from 'node:fs'
import {parse} from 'acorn'
import {mock} from 'bun:test'
import axios from 'axios'
import {lookup} from 'node:dns'
import {isIP} from 'node:net'
import {SandboxManager as BaseSandboxManager} from '@anthropic-ai/sandbox-runtime'
import {createCombinedAbortSignal} from '../../../../src/utils/combinedAbortSignal'
const input=JSON.parse(await Bun.stdin.text())
if(Bun.version!=='1.3.14'||new URL(input.url).hostname!=='127.0.0.1')throw new Error('private pinned transport required')
const read=(name:string)=>{
 const code=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/hooks/'+name,import.meta.url),'utf8'))
 const ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
 const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
 return(names:string[])=>names.map(name=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('missing Source '+name);return code.slice(n.start,n.end)}).join('\n')
}
const policy=read('execHttpHook.ts'),address=read('ssrfGuard.ts')
const ssrfGuardedLookup=new Function('isIP','dnsLookup',address(['ssrfGuardedLookup','ssrfError','isBlockedAddress','isBlockedV4','isBlockedV6','expandIPv6Groups','extractMappedIPv4'])+';return ssrfGuardedLookup')(isIP,lookup)
const adapter=new URL('../../../../src/utils/sandbox/sandbox-adapter.ts',import.meta.url).pathname
let waits=0,ports=0
// Only the adapter's settings/dependency observation is frozen; network state
// and its wait/get methods are the installed original SandboxManager.
mock.module(adapter,()=>({SandboxManager:{isSandboxingEnabled:()=>true,waitForNetworkInitialization:()=>{waits++;return BaseSandboxManager.waitForNetworkInitialization()},getProxyPort:()=>{ports++;return BaseSandboxManager.getProxyPort()}}}))
// Remap the original module import path to its actual Source location. No
// control flow/conditional/body is rewritten.
const proxyCode=policy(['getSandboxProxyConfig']).replace("../sandbox/sandbox-adapter.js",adapter)
const getSandboxProxyConfig=new Function(proxyCode+';return getSandboxProxyConfig')()
const before=await getSandboxProxyConfig()
const execute=new Function('axios','createCombinedAbortSignal','logForDebugging','errorMessage','getProxyUrl','shouldBypassProxy','settingsModule','ssrfGuardedLookup','getSandboxProxyConfig','DEFAULT_HTTP_HOOK_TIMEOUT_MS',policy(['getHttpHookPolicy','urlMatchesPattern','sanitizeHeaderValue','interpolateEnvVars','execHttpHook'])+';return execHttpHook')(axios,createCombinedAbortSignal,()=>{},(e:any)=>e.message,()=>undefined,()=>false,{getInitialSettings:()=>({sandbox:{enabled:true,network:{httpProxyPort:input.configuredProxyPort}}})},ssrfGuardedLookup,getSandboxProxyConfig,600000)
const result=await execute({type:'http',url:input.url},'WorktreeCreate',input.body,new AbortController().signal)
process.stdout.write(JSON.stringify({waits,ports,initialized:await BaseSandboxManager.waitForNetworkInitialization(),proxy:before??null,result})+'\n')
