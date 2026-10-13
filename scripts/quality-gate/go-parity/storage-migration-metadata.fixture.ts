import { assertDisposableFixtureHome } from './fixture-guard'
import { readFile, readdir, chmod } from 'node:fs/promises'
import path from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual managed storage relocation')
if(process.env.CC_HAHA_STORAGE_METADATA_FIXTURE!=='1')throw new Error('private metadata fixture only')
await import('../../../preload')
const {relocateManagedData}=await import('../../../src/utils/storageMigrationMetadata')
let error:string|undefined
try {await relocateManagedData(input.sourceDir,input.targetDir,input.stagedDir)}catch(value){error=value instanceof Error?value.message:String(value)}
for(const [file,mode] of Object.entries(input.restorePermissions??{})){if(!file.startsWith(input.homeDir+path.sep))throw new Error('private permission fixture only');await chmod(file,mode as number)}
const files:Record<string,string>={}
async function walk(directory:string){for(const entry of await readdir(directory,{withFileTypes:true})){const file=path.join(directory,entry.name);if(entry.isDirectory())await walk(file);else if(entry.isFile())files[path.relative(input.stagedDir,file).split(path.sep).join('/')]=(await readFile(file)).toString('base64')}}
await walk(input.stagedDir)
process.stdout.write(JSON.stringify({files,error}))
