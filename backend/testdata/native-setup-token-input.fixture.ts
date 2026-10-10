import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'setup-token input/clipboard')
const input=await Bun.stdin.json()
const {mock}=await import('bun:test')
await import('../../preload')
const calls:any[]=[]
let attempt=0
mock.module(new URL('../../src/utils/execFileNoThrow.ts',import.meta.url).pathname,()=>({execFileNoThrow:async (name:string,args:string[],opts:any)=>{
 calls.push({name,args,input:opts.input,useCwd:opts.useCwd,timeout:opts.timeout})
 const code=input.codes?.[attempt++]??0
 return {code,stdout:'',stderr:''}
}}))
for(const name of ['TERM','TERM_PROGRAM','TMUX','STY','SSH_CONNECTION','LC_TERMINAL','KITTY_WINDOW_ID'])delete process.env[name]
Object.assign(process.env,input.env??{})
Object.defineProperty(process,'platform',{value:input.platform??'darwin'})
const {setClipboard}=await import('../../src/ink/termio/osc')
const raw=await setClipboard(input.text??'url-owned')
// Drain only the owned process primitive promises used by Linux's probe chain.
await new Promise<void>(resolve=>setImmediate(resolve))
const {Cursor}=await import('../../src/utils/Cursor')
const edits=[]
for(const sample of input.edits??[]){
 let cursor=Cursor.fromText(sample.text,sample.columns??80,sample.offset??sample.text.length)
 for(const method of sample.methods??[]){
  const result=(cursor as any)[method]()
  cursor=result?.cursor??result
 }
 edits.push({text:cursor.text,offset:cursor.offset,masked:cursor.render('','*',(text:string)=>text)})
}
console.log(JSON.stringify({raw,calls,edits}))
