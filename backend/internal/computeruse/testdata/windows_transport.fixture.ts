import { join } from 'node:path'
import { readFile, mkdir, writeFile, rm } from 'node:fs/promises'
import { createHash } from 'node:crypto'
const input=await Bun.stdin.json()
const root=input.root
process.env.CLAUDE_CONFIG_DIR=join(root,'config')
// Prime execa's host executable implementation before emulating Windows platform facts.
// The original execFileNoThrow module, subprocess decoder, and bridge remain unchanged.
await import(input.repository+'/node_modules/execa/index.js')
Object.defineProperty(process,'platform',{value:'win32'})
const bridge=await import(input.repository+'/src/utils/computerUse/pythonBridge.ts')
const routed=await import(input.repository+'/src/utils/computerUse/helperBridge.ts')
const badge=await import(input.repository+'/src/utils/computerUse/winCursorBadge.ts')
const results=[]
for(const row of input.rows){try{results.push({result:await (input.routed?routed.callHelper:bridge.callPythonHelper)(row.command,row.payload)})}catch(error){results.push({error:error instanceof Error?error.message:String(error)});if(input.retryAfterFailure)await rm(input.retryAfterFailure,{force:true})}}
if(input.endTurn)badge.hideCursorBadge()
console.log(JSON.stringify({results, tag:bridge.getComputerUsePythonEnv().CC_HAHA_COMPUTER_USE_INPUT_TAG}))
