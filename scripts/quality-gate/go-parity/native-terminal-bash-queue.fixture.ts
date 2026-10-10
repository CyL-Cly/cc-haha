import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Terminal Bash queue')
if(process.env.CC_HAHA_NATIVE_TERMINAL_BASH_QUEUE_FIXTURE!=='1')throw new Error('Unowned terminal Bash queue fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const queue=await import('../../../src/utils/messageQueueManager')
const {processQueueIfReady}=await import('../../../src/utils/queueProcessor')
const rows=input.rows.map((row:any[])=>{
 queue.clearCommandQueue();for(const command of row)queue.enqueue(command)
 const batches:any[]=[]
 while(processQueueIfReady({executeInput:async commands=>{batches.push(commands.map(({value,mode,agentId}:any)=>({value,mode,agentId})))}}).processed){}
 return{batches,remaining:queue.getCommandQueue().map(({value,mode,agentId}:any)=>({value,mode,agentId}))}
})
if(capabilities.blocked.length)throw new Error('Blocked terminal Bash queue fixture:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,capabilities:{blocked:capabilities.blocked}})+'\n')
