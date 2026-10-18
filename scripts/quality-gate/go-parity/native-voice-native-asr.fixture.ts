import { assertDisposableFixtureHome } from './fixture-guard'
import { readFile } from 'node:fs/promises'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual local Voice ASR')
if(process.env.CC_HAHA_VOICE_NATIVE_ASR_FIXTURE!=='1')throw new Error('Missing actual local ASR fixture')
const {createTranscriber,loadSherpa}=await import('../../../src/server/services/voice/sensevoice/worker')
const transcribe=createTranscriber(input.config,loadSherpa(input.config.runtimeRoot))
const wav=new Uint8Array(await readFile(input.wavPath))
const result=transcribe(wav,input.language??'en')
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({result}),()=>resolve()))
process.exit(0)
