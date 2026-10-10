import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import * as fs from 'node:fs/promises'
import * as path from 'node:path'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original bulk partial write')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original bulk writer')
const {useMultiFileAuthState}=await import(baileysLeaf('Utils/use-multi-file-auth-state.js'))
const dir=path.join(process.env.HOME!,'auth');await fs.mkdir(dir,{recursive:true});await fs.mkdir(path.join(dir,'session-bad.json'))
const {state}=await useMultiFileAuthState(dir);let failed=false
try{await state.keys.set({session:{bad:{value:1},good:{value:2}}})}catch{failed=true}
// readData takes the same actual per-file mutex, so this waits for the sibling
// physical write rather than using a sleep or assuming Promise.all completion.
const values=await state.keys.get('session',['good'])
process.stdout.write(JSON.stringify({failed,good:values.good})+'\n')
