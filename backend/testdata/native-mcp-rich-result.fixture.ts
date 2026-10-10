import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { mkdirSync, writeFileSync, readdirSync, readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import * as fileOps from 'node:fs/promises'
import { mock } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'Native MCP rich results')
if (process.env.CC_HAHA_MCP_RICH_RESULT_FIXTURE !== '1') throw new Error('Missing owned MCP rich result fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setOriginalCwd(input.work)
bootstrap.switchSession(input.session)
const { getToolResultsDir } = await import('../../src/utils/toolResultStorage')
const directory = getToolResultsDir()
if (input.obstruct) { mkdirSync(dirname(directory), {recursive:true});writeFileSync(directory,'private obstruction') }
let releaseWrites: ()=>void = ()=>{}
const gate = new Promise<void>(resolve=>{releaseWrites=resolve})
const originalWrite=fileOps.writeFile
const writes: Promise<any>[]=[]
let started=0, completed=0
if(input.parallel)mock.module('fs/promises',()=>({...fileOps,writeFile:(...args:any[])=>{
  if(!String(args[0]).startsWith(directory+'/'))return (originalWrite as any)(...args)
  started++
  const write=(async()=>{await gate;try{return await (originalWrite as any)(...args)}finally{completed++}})()
  writes.push(write)
  return write
}}))
const { transformMCPResult } = await import('../../src/services/mcp/client')
Date.now = () => input.now
let randomIndex = 0
Math.random = () => input.randoms[randomIndex++] ?? 0.5
const rows: any[] = []
for (const result of input.cases) {
  try { const transformed = await transformMCPResult(result, 'contract-tool', input.server);rows.push({content:transformed.content,error:null}) }
  catch (error: any) {rows.push({content:null,error:error.message})}
}
let parallel: any
if(input.parallel){
 let turns=0
 while(started!==2){if(++turns>100000)throw new Error('Actual Source Promise.all did not start both blob writes');await new Promise(resolve=>setImmediate(resolve))}
 const before={started,completed,rejected:rows[0].error!==null}
 releaseWrites()
 await Promise.all(writes)
 parallel={before,after:{started,completed}}
}
const files: any[] = []
if (!input.obstruct) {
  try {for (const name of readdirSync(directory).sort()) files.push({name,data:readFileSync(join(directory,name)).toString('base64')})} catch {}
}
process.stdout.write(JSON.stringify({rows,files,directory,randomIndex,...(parallel?{parallel}:{})}))
