import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual pairing verifier')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original pairing verifier')
const input=await Bun.stdin.json()
const {configureSuccessfulPairing}=await import(baileysLeaf('Utils/validate-connection.js'))
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
const {Curve}=await import(baileysLeaf('Utils/crypto.js'))
const {proto}=await import(baileysLeaf('../WAProto/index.js'))
const revive=(v:any)=>JSON.parse(JSON.stringify(v),BufferJSON.reviver)
const old=revive(input.old),actual=revive(input.actual)
const stanza={tag:'iq',attrs:{id:'private-pair',from:'s.whatsapp.net',type:'set'},content:[{tag:'pair-success',attrs:{},content:[{tag:'device-identity',attrs:{},content:Buffer.from(input.identity,'base64')},{tag:'device',attrs:{jid:'222:3@s.whatsapp.net',lid:'99222:3@lid'}},{tag:'biz',attrs:{name:'Private Business'}},{tag:'platform',attrs:{name:'android'}}]}]}
const {creds,reply}=configureSuccessfulPairing(stanza,old)
const expected={...old,...creds}
const account=proto.ADVSignedDeviceIdentity.fromObject(actual.account)
const deviceMsg=Buffer.concat([Buffer.from([6,1]),account.details,actual.signedIdentityKey.public,account.accountSignatureKey])
const validNativeSignature=Curve.verify(actual.signedIdentityKey.public,deviceMsg,account.deviceSignature)
const signatureBytes=proto.ADVSignedDeviceIdentity.decode(Buffer.from(input.reply,'base64'))
const validReplySignature=Curve.verify(actual.signedIdentityKey.public,deviceMsg,signatureBytes.deviceSignature)
delete expected.account.deviceSignature
delete actual.account.deviceSignature
process.stdout.write(JSON.stringify({expected,actual,validNativeSignature,validReplySignature,replyTag:reply.content[0].tag,keyIndex:reply.content[0].content[0].attrs['key-index']},BufferJSON.replacer)+'\n')
