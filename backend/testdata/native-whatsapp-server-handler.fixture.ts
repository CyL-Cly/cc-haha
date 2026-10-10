import {readFileSync} from 'node:fs'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual server handler')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original server handler')
const rows=await Bun.stdin.json()
const text=readFileSync(new URL('../../adapters/whatsapp/index.ts',import.meta.url),'utf8')
const start=text.indexOf('async function flushAccumulatedText'),end=text.indexOf('async function startNewSession',start)
if(start<0||end<0)throw new Error('Original WhatsApp handler missing')
const {syncImPermissionState}=await import('../../adapters/common/permission-sync.ts')
const {formatPermissionRequest}=await import('../../adapters/common/format.ts')
const {formatPermissionInstructions}=await import('../../adapters/common/permission.ts')
const {formatWhatsAppOutboundText,buildWhatsAppThinkingPreview}=await import('../../adapters/whatsapp/format.ts')
const source=new Bun.Transpiler({loader:'ts'}).transformSync(text.slice(start,end))
const texts:string[]=[],runtime:any={state:'idle',pendingPermissionCount:0}
const accumulatedText=new Map(),accumulatedThinkingText=new Map(),thinkingNotices=new Set(),pendingPermissions=new Map()
const build=new Function('getRuntimeState','syncImPermissionState','pendingPermissions','thinkingNotices','sendWhatsAppText','accumulatedText','accumulatedThinkingText','getImageWatcher','dispatchOutboundMedia','buildWhatsAppThinkingPreview','formatPermissionRequest','formatPermissionInstructions','sessionStore','defaultWorkDir','clearTransientChatState','bridge','createSessionForChat','formatWhatsAppOutboundText',source+';return handleServerMessage')
const run=build(()=>runtime,syncImPermissionState,pendingPermissions,thinkingNotices,async(_id:string,body:string)=>texts.push(body),accumulatedText,accumulatedThinkingText,()=>({feed(){return[]}}),async()=>{},buildWhatsAppThinkingPreview,formatPermissionRequest,formatPermissionInstructions,{get(){return null},delete(){}},'',()=>{}, {resetSession(){}},async()=>false,formatWhatsAppOutboundText)
for(const row of rows)await run('private',row)
process.stdout.write(JSON.stringify({texts,state:runtime,pending:[...(pendingPermissions.get('private')??[])]})+'\n')
