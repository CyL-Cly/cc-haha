import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual credential schema')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned credential fixture')
const input=await Bun.stdin.json()
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
const {Curve,generateSignalPubKey}=await import(baileysLeaf('Utils/crypto.js'))
const {initAuthCreds}=await import(baileysLeaf('Utils/auth-utils.js'))
const creds=JSON.parse(JSON.stringify(input.creds),BufferJSON.reviver)
const expected=initAuthCreds()
const proof={sameFields:JSON.stringify(Object.keys(creds).sort())===JSON.stringify(Object.keys(expected).filter(k=>expected[k]!==undefined).sort()),validSignedPrekey:Curve.verify(creds.signedIdentityKey.public,generateSignalPubKey(creds.signedPreKey.keyPair.public),creds.signedPreKey.signature),registrationInSourceRange:creds.registrationId>=0&&creds.registrationId<=255,defaults:{registered:creds.registered,nextPreKeyId:creds.nextPreKeyId,firstUnuploadedPreKeyId:creds.firstUnuploadedPreKeyId,accountSyncCounter:creds.accountSyncCounter,accountSettings:creds.accountSettings},keyLengths:{noisePrivate:creds.noiseKey.private.length,noisePublic:creds.noiseKey.public.length,signature:creds.signedPreKey.signature.length,advSecret:Buffer.from(creds.advSecretKey,'base64').length}}
process.stdout.write(JSON.stringify(proof)+'\n')
