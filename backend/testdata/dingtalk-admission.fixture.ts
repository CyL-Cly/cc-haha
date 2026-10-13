import { adapterMigrationLifecycle, registerAdapterShutdown } from '../../adapters/common/migration-lifecycle.js'
import { WsBridge } from '../../adapters/common/ws-bridge.js'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
if (process.env.CC_HAHA_DINGTALK_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const root=new URL('../../',import.meta.url),input=JSON.parse(await Bun.stdin.text())
const helpers=await import(new URL('adapters/dingtalk/helpers.ts',root).href)
const {parseDingTalkPermissionCardAction}=await import(new URL('adapters/dingtalk/permission-card.ts',root).href)
const {DWClient}=await import(new URL('adapters/node_modules/dingtalk-stream/dist/client.mjs',root).href)
const {TOPIC_ROBOT,TOPIC_CARD}=await import(new URL('adapters/node_modules/dingtalk-stream/dist/constants.mjs',root).href)
const {MessageDedup}=await import(new URL('adapters/common/message-dedup.ts',root).href)
const source=readFileSync(fileURLToPath(new URL('adapters/dingtalk/index.ts',root)),'utf8')
const start=source.indexOf('async function handleRobotMessage('),end=source.indexOf('\nasync function collectAttachments(',start)
const body=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(start,end))
const sessionWebhooks=new Map(),aiCardTargets=new Map(),pendingPermissions=new Map([['dingtalk:dm:U',new Set(['r'])]]),pendingPermissionChats=new Map([['r','dingtalk:dm:U']])
const order=[],wires=[],decisions=[]
let release
const held=new Promise(resolve=>{release=resolve})
const scope={...helpers,parseDingTalkPermissionCardAction,sessionWebhooks,aiCardTargets,pendingPermissions,pendingPermissionChats,
 isAllowedUser:()=>true,tryPair:()=>false,
 collectAttachments:async(chat,candidates)=>{if(candidates.length){order.push('collect:held');await held;return ['physical attachment']}return []},
 routeUserMessage:async(chat,text,attachments)=>{order.push('route:'+text)},
 applyPermissionDecision:(chat,decision)=>{decisions.push(decision);pendingPermissions.get(chat).delete(decision.requestId);pendingPermissionChats.delete(decision.requestId);return true},
 sendText:async()=>{},formatPermissionDecisionStatus:()=>'',console:{warn(){}},
}
const handlers=new Function(...Object.keys(scope),body+';return {handleRobotMessage,handleCardCallback}')(...Object.values(scope))
const begin=source.indexOf('  const client = new DWClient(',source.indexOf('async function start()')),finish=source.indexOf('\n  await client.connect()',begin)
const listener=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(begin,finish))
const dedup=new MessageDedup()
const bridge=new WsBridge('ws://127.0.0.1:1','dingtalk')
const client=new Function('DWClient','config','TOPIC_ROBOT','TOPIC_CARD','dedup','parseDingTalkPayload','handleRobotMessage','handleCardCallback','registerAdapterShutdown','adapterMigrationLifecycle','bridge',listener+';return client')(
 DWClient,{dingtalk:{clientId:'id',clientSecret:'secret'}},TOPIC_ROBOT,TOPIC_CARD,dedup,helpers.parseDingTalkPayload,handlers.handleRobotMessage,handlers.handleCardCallback,registerAdapterShutdown,adapterMigrationLifecycle,bridge,
)
client.socket={send(w){wires.push(JSON.parse(w))}}
for(const frame of input.frames)client.onDownStream(JSON.stringify(frame))
const prefix={webhooks:[...sessionWebhooks],targets:[...aiCardTargets],pending:[...pendingPermissionChats],decisions:structuredClone(decisions)}
await Promise.resolve();await Promise.resolve();const beforeRelease=structuredClone(order)
release();await Promise.resolve();await Promise.resolve();await Promise.resolve()
dedup.destroy();bridge.destroy();process.stdout.write(JSON.stringify({prefix,wires,beforeRelease,order}))
