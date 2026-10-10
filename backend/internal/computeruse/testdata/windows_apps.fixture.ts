import { readFile, writeFile, mkdir, readdir } from "node:fs/promises"
import { join } from "node:path"
import { createHash } from "node:crypto"
const input = await Bun.stdin.json()
Object.defineProperty(process, "platform", {value:"win32"})
const root = input.root
const config = join(root, "config")
const state = join(config, ".runtime")
await mkdir(config, {recursive:true})
await mkdir(join(config,"cc-haha"), {recursive:true})
await writeFile(join(config,"cc-haha/computer-use-config.json"), JSON.stringify({enabled:false, authorizedApps:[], pythonPath:input.custom ?? null}))
for (const [name,body] of Object.entries(input.files ?? {})) {const path=join(state,name);await mkdir(join(path,".."),{recursive:true});await writeFile(path,String(body))}
const calls=[];let ordinal=0
const expand=(v)=>v.replaceAll("$ROOT",root)
Bun.spawn=(args)=>{
 const row=input.plan[ordinal++];calls.push(args.map(v=>v.replaceAll(root,"$ROOT")))
 if(!row || JSON.stringify(args)!==JSON.stringify([row.command,...row.args].map(expand))) throw new Error("Unexpected fixture command "+JSON.stringify(args))
 const exited=(async()=>{if(row.createVenv){const dir=join(state,"venv/Scripts");await mkdir(dir,{recursive:true});await writeFile(join(dir,"python.exe"),"owned-venv");if(!row.noPip)await writeFile(join(dir,"pip.exe"),"owned-pip")};return row.code ?? 0})()
 return {stdout:new Response(row.stdout ?? "").body,stderr:new Response(row.stderr ?? "").body,exited}
}
Bun.plugin({name:"export-original-private-setup",setup(build){build.onLoad({filter:/[/\\]server[/\\]api[/\\]computer-use\.ts$/},async({path})=>({contents:(await readFile(path,"utf8"))+"\nexport {runSetup}\n",loader:"ts"}))}})
process.env.CLAUDE_CONFIG_DIR=config
const api=await import(input.repository+"/src/server/api/computer-use.ts")
const url=new URL("http://fixture.invalid/api/computer-use/apps")
const response=await api.handleComputerUseApi(new Request(url),url,["api","computer-use","apps"])
const result={status:response.status,body:await response.json()}
const files={}
async function scan(dir,relative=""){let entries;try{entries=await readdir(dir,{withFileTypes:true})}catch{return};for(const e of entries){const name=relative?relative+"/"+e.name:e.name;const full=join(dir,e.name);if(e.isDirectory())await scan(full,name);else files[name]=createHash("sha256").update(await readFile(full)).digest("hex")}}
await scan(state)
console.log(JSON.stringify({result,calls,files,consumed:ordinal}))
