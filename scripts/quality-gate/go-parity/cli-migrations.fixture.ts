import fs, * as fsExports from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { syncBuiltinESMExports } from 'node:module'
import { mock, spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { parse } from 'acorn'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['home','name','global','user','local','changelog','env','override','fault','faultStart','schema','twice'].includes(key))) throw new Error('Migrations require declared private inputs before production imports')
const home = assertDisposableFixtureHome(input.home, 'CLI migrations')
if (!feature('TRANSCRIPT_CLASSIFIER') || process.env.NODE_ENV !== 'production' || process.env.USER_TYPE !== 'external' || process.env.CC_HAHA_CLI_MIGRATIONS_FIXTURE !== '1' || input.fault !== undefined && !['user','local','global','changelog'].includes(input.fault)) throw new Error('Migrations require explicit release inputs before production imports')
const allowedEnv = ['CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP','CLAUDE_CODE_DISABLE_1M_CONTEXT']
if (Object.keys(input.env ?? {}).some(key => !allowedEnv.includes(key))) throw new Error('Migrations require original declared environment before production imports')
Object.assign(process.env, input.env ?? {})
const root = resolve(import.meta.dir, '../../..'), project = join(home,'project'), configHome = join(home,'.claude'), globalPath = join(configHome,'.claude.json'), userPath = join(configHome,'settings.json'), localPath = join(project,'.claude/settings.local.json'), cachePath = join(configHome,'cache/changelog.md')
const real = {...fs}, effects: any[] = []
const short = (path: string) => relative(home,path)
const now = 946684800000, RealDate = Date
class FixtureDate extends RealDate { constructor(...args: any[]) { super(...(args.length ? args : [now]) as [any]) }; static now() { return now } }
globalThis.Date = FixtureDate as DateConstructor
Object.defineProperty(process,'pid',{value:12345})
fs.mkdirSync(join(project,'.claude'),{recursive:true}); fs.mkdirSync(configHome,{recursive:true})
for(const [path,raw] of [[globalPath,input.global ?? '{}'],[userPath,input.user],[localPath,input.local],[cachePath,input.changelog]]) if(raw !== undefined){fs.mkdirSync(dirname(path),{recursive:true});fs.writeFileSync(path,raw.replaceAll('<HOME>',home),{mode:0o640})}
const absentGit: string[] = []
for(const allocation of [home,real.realpathSync(home)])for(let current=dirname(allocation);;current=dirname(current)){absentGit.push(join(current,'.git'));if(dirname(current)===current)break}
const capabilities = await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),...absentGit]})
const owners = new Map<number,string>(), checkedOpen = fs.openSync, checkedWrite = fs.writeFileSync, checkedClose = fs.closeSync
const relevant = (path: string) => [globalPath,userPath,localPath].some(target => path===target || path.startsWith(target+'.tmp.'))
let faultWrites=0
const fault = (path: string) => {const matching=input.fault === 'global' && path.startsWith(globalPath) || input.fault === 'user' && path.startsWith(userPath) || input.fault === 'local' && path.startsWith(localPath);return matching && ++faultWrites >= (input.faultStart??1)}
const open = (...args:any[]) => {const fd=checkedOpen(...args as [any,any,any]);owners.set(fd,String(args[0]));return fd}
const write = (...args:any[]) => {const path=typeof args[0]==='number'?owners.get(args[0]):String(args[0]);if(!path)throw new Error('Unowned migration descriptor');if(relevant(path)){effects.push({kind:'write',path:short(path),rawBase64:Buffer.from(args[1]).toString('base64')});if(fault(path)){const error=new Error('private fixture write fault') as NodeJS.ErrnoException;error.code='EIO';throw error}}return checkedWrite(...args as [any,any,any])}
const close = (fd:number) => {owners.delete(fd);return checkedClose(fd)}
for(const [name,fn]of Object.entries({openSync:open,writeFileSync:write,closeSync:close})){(fs as any)[name]=fn;spyOn(fsExports as any,name).mockImplementation(fn)}
fs.watchFile = (()=>({})) as any; fs.unwatchFile = (()=>{}) as any
syncBuiltinESMExports()
let releaseAsync!:()=>void
const gate = new Promise<void>(resolve=>{releaseAsync=resolve})
const checkedMkdir = fs.promises.mkdir, checkedAsyncWrite = fs.promises.writeFile
fs.promises.mkdir = (async(...args:any[])=>{const result=await checkedMkdir(...args as [any,any]);if(String(args[0])===dirname(cachePath)){effects.push({kind:'asyncMkdir',path:short(String(args[0]))});await gate}return result}) as any
fs.promises.writeFile = (async(...args:any[])=>{if(String(args[0])===cachePath){effects.push({kind:'asyncWrite',path:short(cachePath),value:typeof args[1]==='string'?args[1]:args[1],options:args[2]});if(input.fault==='changelog')throw new Error('private fixture changelog fault')}return checkedAsyncWrite(...args as [any,any,any])}) as any
mock.module('fs',()=>({...fs,default:fs}));mock.module('fs/promises',()=>({...fs.promises,default:fs.promises}))
await import(join(root,'preload.ts'))
const config=await import(join(root,'src/utils/config.ts')), settings=await import(join(root,'src/utils/settings/settings.ts')), bootstrap=await import(join(root,'src/bootstrap/state.ts')), release=await import(join(root,'src/utils/releaseNotes.ts'))
bootstrap.setOriginalCwd(project);bootstrap.setMainLoopModelOverride(input.override)
config.enableConfigs()
const analytics=await import(join(root,'src/services/analytics/index.ts')), log=await import(join(root,'src/utils/log.ts'))
const logEvent=analytics.logEvent,logError=log.logError
spyOn(analytics,'logEvent').mockImplementation((name:any,data:any)=>{if(/migrat|reset_pro_to_opus/.test(name))effects.push({kind:'event',name,data});logEvent(name,data)})
spyOn(log,'logError').mockImplementation((error:any)=>{effects.push({kind:'error',message:String(error)});logError(error)})
const save=config.saveGlobalConfig,saveProject=config.saveCurrentProjectConfig,update=settings.updateSettingsForSource
spyOn(config,'saveGlobalConfig').mockImplementation((updater:any)=>{effects.push({kind:'saveGlobal'});save((current:any)=>{const next=updater(current);effects.push({kind:'updaterGlobal',before:JSON.stringify(current),after:JSON.stringify(next),same:next===current,lockExists:real.existsSync(globalPath+'.lock')});return next})})
spyOn(config,'saveCurrentProjectConfig').mockImplementation((updater:any,cwd?:string)=>{effects.push({kind:'saveProject'});saveProject((current:any)=>{const next=updater(current);effects.push({kind:'updaterProject',before:JSON.stringify(current),after:JSON.stringify(next),same:next===current,lockExists:real.existsSync(globalPath+'.lock')});return next},cwd)})
spyOn(settings,'updateSettingsForSource').mockImplementation((source:any,patch:any,override?:string)=>{effects.push({kind:'settingsUpdate',source,patch:JSON.stringify(patch),undefinedKeys:Object.keys(patch).filter(key=>patch[key]===undefined)});const result=update(source,patch,override);effects.push({kind:'settingsResult',error:result.error?String(result.error):null});return result})
let pending: Promise<void>|undefined
const changelog=release.migrateChangelogFromConfig
spyOn(release,'migrateChangelogFromConfig').mockImplementation(()=>{effects.push({kind:'changelogStart'});pending=changelog();return pending})

// Compile the actual whole main module without executing it; AST spans select
// the original function and dependencies. No oracle sequence is re-authored.
const source=real.readFileSync(join(root,'src/main.tsx'),'utf8')
const built=await Bun.build({entrypoints:[join(root,'src/main.tsx')],target:'bun',features:['TRANSCRIPT_CLASSIFIER'],external:['*']})
if(!built.success)throw new Error('Pinned actual main compilation failed: '+built.logs.join('\n'))
const compiled=await built.outputs[0]!.text()
const ast=parse(compiled,{ecmaVersion:'latest',sourceType:'module'}) as any
const declaration=ast.body.find((node:any)=>node.type==='FunctionDeclaration'&&node.id.name==='runMigrations')
const constant=ast.body.find((node:any)=>node.type==='VariableDeclaration'&&node.declarations.some((item:any)=>item.id.name==='CURRENT_MIGRATION_VERSION'))
if(!declaration||!constant)throw new Error('Actual migration Source span missing')
const identifiers=new Set<string>()
const visit=(node:any)=>{if(!node||typeof node!=='object')return;if(node.type==='Identifier')identifiers.add(node.name);for(const value of Object.values(node))if(Array.isArray(value))value.forEach(visit);else if(value&&typeof value==='object')visit(value)}
visit(declaration)
const bindings:any[]=[]
for(const node of ast.body.filter((n:any)=>n.type==='ImportDeclaration'))for(const spec of node.specifiers)if(identifiers.has(spec.local.name))bindings.push({local:spec.local.name,imported:spec.imported?.name,path:node.source.value})
for(const binding of bindings.filter(binding=>binding.local!=='migrateChangelogFromConfig'&&/migrate|resetPro|resetAuto/.test(binding.local))){const path=resolve(root,'src',binding.path);const module=await import(path);const original=module[binding.imported];spyOn(module,binding.imported).mockImplementation((...args:any[])=>{effects.push({kind:'migrationStart',name:binding.local});const result=original(...args);effects.push({kind:'migrationEnd',name:binding.local});return result})}
const imports=bindings.map(binding=>`import {${binding.imported} as ${binding.local}} from ${JSON.stringify(binding.path==='bun:bundle'?binding.path:resolve(root,'src',binding.path))}`).join('\n')
const generated=join(home,'actual-run-migrations.ts')
real.writeFileSync(generated,imports+'\n'+compiled.slice(constant.start,constant.end)+'\nexport '+compiled.slice(declaration.start,declaration.end))
const dependencyValues=[]
for(const binding of bindings){const module=await import(binding.path==='bun:bundle'?binding.path:resolve(root,'src',binding.path));dependencyValues.push(module[binding.imported])}
const actualRun={runMigrations:new Function(...bindings.map(binding=>binding.local),compiled.slice(constant.start,constant.end)+'\n'+compiled.slice(declaration.start,declaration.end)+'\nreturn runMigrations')(...dependencyValues)}
const sourceOnly={mainHash:createHash('sha256').update(source).digest('hex'),compiledHash:createHash('sha256').update(compiled).digest('hex'),spans:{constant:[constant.start,constant.end],run:[declaration.start,declaration.end]},bindings,extracted:compiled.slice(declaration.start,declaration.end)}
const files=()=>{const backupDir=join(configHome,'backups');const paths=[globalPath,userPath,localPath,cachePath,...(real.existsSync(backupDir)?real.readdirSync(backupDir).map(name=>join(backupDir,name)):[])];return Object.fromEntries(paths.map(path=>[short(path),real.existsSync(path)?{rawBase64:real.readFileSync(path).toString('base64'),mode:real.statSync(path).mode&0o777}:null]))}
const snapshot=()=>({global:JSON.stringify(config.getGlobalConfig()),override:bootstrap.getMainLoopModelOverride()??null,disableAutoUpdater:process.env.DISABLE_AUTOUPDATER??null,files:files(),lockExists:real.existsSync(globalPath+'.lock')})
effects.length=0
let result:any
if(input.schema){const validation=await import(join(root,'src/utils/settings/validation.ts')),types=await import(join(root,'src/utils/settings/types.ts'));const value=JSON.parse(input.schema);const filtered=validation.filterInvalidPermissionRules(value,userPath);const parsed=types.SettingsSchema().safeParse(filtered.data);result={success:parsed.success,settings:parsed.success?JSON.stringify(parsed.data):null,errors:parsed.success?filtered.warnings:[...filtered.warnings,...validation.formatZodError(parsed.error,userPath)]}}
else{let error:any=null;try{actualRun.runMigrations()}catch(caught){error=String(caught)}const sync=snapshot();releaseAsync();if(pending)await pending.catch(()=>{});const complete=snapshot();if(input.twice)actualRun.runMigrations();if(pending)await pending.catch(()=>{});result={error,sync,complete,final:snapshot(),effects}}
process.stdout.write(JSON.stringify({name:input.name,result,capabilities,sourceOnly}))
