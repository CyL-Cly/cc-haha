import {readFile} from 'node:fs/promises'
import {spawn} from 'node:child_process'
const input=await Bun.stdin.json()
await import(input.repository+'/node_modules/execa/index.js')
Object.defineProperty(process,'platform',{value:'win32'})
const bridge=await import(input.repository+'/src/utils/computerUse/pythonBridge.ts')
const badge=await import(input.repository+'/src/utils/computerUse/winCursorBadge.ts')
await badge.showCursorBadge('get_app_state',{pid:42},{platform:'win32',bootstrap:bridge.ensureBootstrapped,command:()=>({python:input.python,script:input.script}),environment:bridge.getComputerUsePythonEnv,spawnChild:spawn})
const deadline=Date.now()+30000
while(true){try{await readFile(input.marker+'/ready');break}catch{};try{const error=await readFile(input.marker+'/broken','utf8');throw new Error(error)}catch(e){if(e.code!=='ENOENT')throw e};if(Date.now()>deadline)throw new Error('original badge did not emit late READY');await new Promise(resolve=>setTimeout(resolve,1))}
const running=badge.__cursorBadgeIsRunning()
badge.hideCursorBadge()
console.log(JSON.stringify({running,ready:true}))
