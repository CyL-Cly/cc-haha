import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Local user presentation')
if(process.env.CC_HAHA_LOCAL_USERS_FIXTURE!=='1')throw new Error('Unowned local users fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const {mock}=await import('bun:test')
mock.module('react/compiler-runtime',()=>({c:(n:number)=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))}))
mock.module('../../../../src/components/MessageResponse',()=>({MessageResponse:(props:unknown)=>props}))
mock.module('../../../../src/components/InterruptedByUser',()=>({InterruptedByUser:()=>null}))
// Child layout components are framework seams. UserTextMessage's actual
// original dispatch/visibility branches and returned props remain unchanged.
for(const [path,name] of [
 ['../../../../src/components/messages/UserPromptMessage','UserPromptMessage'],
 ['../../../../src/components/messages/UserCommandMessage','UserCommandMessage'],
 ['../../../../src/components/messages/UserAgentNotificationMessage','UserAgentNotificationMessage'],
 ['../../../../src/components/messages/UserBashInputMessage','UserBashInputMessage'],
 ['../../../../src/components/messages/UserBashOutputMessage','UserBashOutputMessage'],
 ['../../../../src/components/messages/UserLocalCommandOutputMessage','UserLocalCommandOutputMessage'],
 ['../../../../src/components/messages/UserMemoryInputMessage','UserMemoryInputMessage'],
 ['../../../../src/components/messages/UserPlanMessage','UserPlanMessage'],
 ['../../../../src/components/messages/UserResourceUpdateMessage','UserResourceUpdateMessage'],
 ['../../../../src/components/messages/UserTeammateMessage','UserTeammateMessage'],
] as const){mock.module(path,()=>({[name]:Object.defineProperty((props:unknown)=>props,'name',{value:name}),...(name==='UserTeammateMessage'?{TeammateMessageContent:(props:unknown)=>props}:{})}))}
const {UserTextMessage}=await import('../../../../src/components/messages/UserTextMessage')
const visible=[]
for(const row of input.records){
 if(row.type!=='user'||row.isMeta)continue
 const result=UserTextMessage({param:{type:'text',text:row.message.content},addMargin:false,verbose:false})
 if(result!==null){
  if(result.type.name!=='UserPromptMessage')throw new Error('Unexpected original local user presentation:'+result.type.name)
  visible.push(result.props.param.text)
 }
}
if(capabilities.blocked.length)throw new Error('Blocked original local users:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({visible})+'\n')
