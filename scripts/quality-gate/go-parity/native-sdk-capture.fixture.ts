import {spyOn} from 'bun:test'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'SDK capture')
if(process.env.CC_HAHA_SDK_CAPTURE_FIXTURE!=='1')throw new Error('Missing SDK capture fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
spyOn(console,'log').mockImplementation(()=>{})
spyOn(console,'error').mockImplementation(()=>{})
const {ConversationService}=await import('../../../src/server/services/conversationService')
const service:any=new ConversationService()
const forwarded:any[]=[],session:any={sdkMessages:[],pendingPermissionRequests:new Map(),outputCallbacks:[(message:any)=>forwarded.push(message)],sdkToken:'private-fixture-token',sdkSocket:null,pendingOutbound:[],workDir:input.homeDir}
service.sessions.set('fixture',session)
for(const payload of input.payloads)service.handleSdkPayload('fixture',payload)
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({recent:service.getRecentSdkMessages('fixture'),init:service.getSessionInitMessage('fixture'),forwarded,seen:session.seenSdkMessageUuids.size,capabilities}),()=>resolve()))
process.exit(0)
