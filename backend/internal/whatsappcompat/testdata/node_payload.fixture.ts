import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original handshake payload')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original payload')
const input=await Bun.stdin.json()
const {generateLoginNode,generateRegistrationNode}=await import(baileysLeaf('Utils/validate-connection.js'))
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
const {proto}=await import(baileysLeaf('../WAProto/index.js'))
const creds=JSON.parse(JSON.stringify(input.creds),BufferJSON.reviver)
const config={browser:['cc-haha','desktop','1.0'],syncFullHistory:false,countryCode:'US',version:input.version}
const payload=creds.me?.id?generateLoginNode(creds.me.id,config):generateRegistrationNode(creds,config)
const normalize=(p:any)=>{const value=proto.ClientPayload.toObject(p,{bytes:String,longs:String});if(p.devicePairingData?.deviceProps)value.devicePairingData.deviceProps=proto.DeviceProps.toObject(proto.DeviceProps.decode(p.devicePairingData.deviceProps),{bytes:String,longs:String});return value}
process.stdout.write(JSON.stringify({expected:normalize(payload),actual:normalize(proto.ClientPayload.decode(Buffer.from(input.actual,'base64')))})+'\n')
