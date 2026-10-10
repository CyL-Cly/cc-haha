import { assertDisposableFixtureHome } from './fixture-guard'
import { spawn } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Native Voice recognizer lifecycle')
if(process.env.CC_HAHA_VOICE_RECOGNIZER_FIXTURE!=='1')throw new Error('Missing recognizer fixture')
const {SenseVoiceRecognizer}=await import('../../../src/server/services/voice/sensevoice/recognizer')
const fixture=new URL('../../../src/server/services/voice/sensevoice/__fixtures__/fakeWorker.mjs',import.meta.url)
const config={runtimeRoot:input.homeDir,model:join(input.homeDir,'model.onnx'),tokens:join(input.homeDir,'tokens.txt'),vad:join(input.homeDir,'vad.onnx'),threads:1,maxAudioBytes:1024,vadThreshold:0.5,minSilenceSeconds:0.5,minSpeechSeconds:0.25,segmentSeconds:30}
const recognizer=new SenseVoiceRecognizer({cwd:input.homeDir,workerConfig:()=>config,spawnWorker:({cwd,env})=>spawn(process.execPath,[fixture.pathname],{cwd,env,stdio:'pipe'}),idleTimeoutMs:0,inferenceTimeoutMs:60,killGraceMs:100})
const rows=[]
for(const language of input.languages){try{rows.push({result:await recognizer.transcribe(new Uint8Array(64),language,new AbortController().signal)})}catch(error){rows.push({message:(error as Error).message,code:(error as any).code,status:(error as any).status})}}
await recognizer.dispose()
const starts=await readFile(join(input.homeDir,'starts.log'),'utf8').catch(()=> '')
const requests=await readFile(join(input.homeDir,'requests.log'),'utf8').catch(()=> '')
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({rows,starts:starts.trim().split('\n').filter(Boolean).length,requests:requests.trim().split('\n').filter(Boolean).map(line=>line.split(' ')[1])}),()=>resolve()))
process.exit(0)
