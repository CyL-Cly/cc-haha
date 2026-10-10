import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual Node Signal records')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned WhatsApp Signal fixture')
const req=createRequire(import.meta.url),SessionRecord=req(signalLeaf('session_record.js')),curve=req(signalLeaf('curve.js')),crypto=req(signalLeaf('crypto.js'))
const record=new SessionRecord(),local=curve.generateKeyPair(),remote=curve.generateKeyPair()
const mkSeed=Buffer.alloc(32,0x41),derived=crypto.deriveSecrets(mkSeed,Buffer.alloc(32),Buffer.from('WhisperMessageKeys'))
for(let i=0;i<2;i++){const entry=SessionRecord.createEntry(),pair=curve.generateKeyPair(),base=curve.generateKeyPair();entry.registrationId=123;entry.currentRatchet={ephemeralKeyPair:pair,lastRemoteEphemeralKey:remote.pubKey,previousCounter:2,rootKey:Buffer.alloc(32,0x11+i)};entry.indexInfo={baseKey:base.pubKey,baseKeyType:1,closed:i===0?-1:1111,used:2222-i,created:1000,remoteIdentityKey:remote.pubKey};entry.addChain(pair.pubKey,{chainKey:{counter:3,key:Buffer.alloc(32,0x22+i)},chainType:1,messageKeys:{}});entry.addChain(remote.pubKey,{chainKey:{counter:4,key:Buffer.alloc(32,0x33+i)},chainType:2,messageKeys:{3:mkSeed}});entry.pendingPreKey={signedKeyId:1,preKeyId:7,baseKey:base.pubKey};record.setSession(entry)}
const serialized=record.serialize(),again=SessionRecord.deserialize(JSON.parse(JSON.stringify(serialized))).serialize()
process.stdout.write(JSON.stringify({record:serialized,roundtrip:again,localPublic:local.pubKey,messageSeed:mkSeed,derived:{cipherKey:derived[0],macKey:derived[1],iv:derived[2].slice(0,16)}},BufferJSON.replacer)+'\n')
