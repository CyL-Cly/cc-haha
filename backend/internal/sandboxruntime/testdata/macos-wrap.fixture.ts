import fs from 'node:fs'
import {createRequire} from 'node:module'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text()),home=assertDisposableFixtureHome(input.home,'actual Sandbox macOS wrapper')
process.chdir(input.cwd)
Math.random=()=>0.25
const {wrapCommandWithSandboxMacOS}=await import('@anthropic-ai/sandbox-runtime/dist/sandbox/macos-sandbox-utils.js')
const {generateProxyEnvVars}=await import('@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-utils.js')
const suffix='_'+(0.25).toString(36).slice(2,11)+'_SBX'
const rows=[]
for(const row of input.rows){
 let error:string|null=null,result:string|null=null
 try {result=wrapCommandWithSandboxMacOS({...row,readConfig:row.read,writeConfig:row.write})}catch(e:any){error=e.message}
 rows.push({result,error,environment:generateProxyEnvVars(row.httpProxyPort,row.socksProxyPort)})
}
process.stdout.write(JSON.stringify({suffix,rows})+'\n')
