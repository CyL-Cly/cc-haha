import { readFileSync, statSync, lstatSync, realpathSync } from 'node:fs'
import { resolve, win32, posix } from 'node:path'
import { parse } from 'acorn'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Windows permission startup')
if (process.env.CC_HAHA_WINDOWS_STARTUP_FIXTURE !== '1') throw new Error('private Windows startup fixture only')
const root = resolve(import.meta.dir, '../..')
const transpile = (file:string) => new Bun.Transpiler({loader:'ts'}).transformSync(readFileSync(`${root}/src/${file}`, 'utf8'))
function functions(file:string, names:string[]) {
  const code = transpile(file), ast:any = parse(code,{ecmaVersion:'latest',sourceType:'module'})
  return names.map(name => {
    let found:any
    for(let node of ast.body) { if(node.type==='ExportNamedDeclaration')node=node.declaration; if(node?.type==='FunctionDeclaration'&&node.id.name===name)found=node }
    if(!found)throw new Error(`Missing actual Source ${file}:${name}`)
    return code.slice(found.start,found.end)
  }).join('\n')
}
const windows = transpile('utils/windowsPaths.ts'), windowsAST:any = parse(windows,{ecmaVersion:'latest',sourceType:'module'})
const conversions:string[]=[]
for(let node of windowsAST.body){if(node.type==='ExportNamedDeclaration')node=node.declaration;if(node?.type==='VariableDeclaration'&&node.declarations.some((d:any)=>['windowsPathToPosixPath','posixPathToWindowsPath'].includes(d.id.name)))conversions.push(windows.slice(node.start,node.end))}
if(conversions.length!==2)throw new Error('Missing actual Windows conversion exports')
const code = [
  ...conversions,
  functions('utils/path.ts',['expandPath','containsPathTraversal']),
  functions('utils/permissions/filesystem.ts',['relativePath','normalizeCaseForComparison','pathInWorkingPath','allWorkingDirectories']),
  functions('utils/fsOperations.ts',['safeResolvePath']),
  functions('utils/errors.ts',['getErrnoCode']),
  functions('commands/add-dir/validation.ts',['validateDirectoryForWorkspace','addDirHelpMessage']),
  functions('utils/permissions/PermissionUpdate.ts',['applyPermissionUpdate','applyPermissionUpdates']),
  functions('utils/permissions/permissions.ts',['convertRulesToUpdates','applyPermissionRulesToPermissionContext']),
  functions('utils/permissions/permissionsLoader.ts',['loadAllPermissionRulesFromDisk']),
  functions('utils/permissions/permissionSetup.ts',['parseToolListFromCLI','isSymlinkTo','isAutoModeDisabledBySettings','isAutoModeGateEnabled','initializeToolPermissionContext']),
].join('\n')
const rows=[]
for(const row of input.rows) {
  const accessed:string[]=[]
  const mapPath=(path:string)=>{
    accessed.push(path)
    const failure=row.failures?.[path]
    if(failure)throw Object.assign(new Error(`private ${failure}`),{code:failure})
    const key=Object.keys(input.paths).find(key=>key.toLowerCase()===path.toLowerCase())
    const disk=key?input.paths[key]:undefined
    if(!disk)throw Object.assign(new Error('private absent path'),{code:'ENOENT'})
    if(!disk.startsWith(input.home+'/'))throw new Error('OS primitive escaped disposable allocation')
    return disk
  }
  const fs={cwd:()=>input.cwd,lstatSync:(path:string)=>lstatSync(mapPath(path)),realpathSync:(path:string)=>{
    realpathSync(mapPath(path))
    return row.canonical?.[path]??path
  }}
  const dependencies={
    memoizeWithLRU:(fn:unknown)=>fn,getPlatform:()=> 'windows',getCwd:()=>input.cwd,homedir:()=>input.nativeHome,
    getFsImplementation:()=>fs,normalize:win32.normalize,isAbsolute:win32.isAbsolute,join:win32.join,resolve:(...paths:string[])=>win32.resolve(input.cwd,...paths),dirname:win32.dirname,posix,
    getOriginalCwd:()=>input.cwd,stat:async(path:string)=>statSync(mapPath(path)),chalk:{bold:(s:string)=>s},
    getSettings_DEPRECATED:()=>row.settings??{},autoModeStateModule:null,feature:(name:string)=>name==='TRANSCRIPT_CLASSIFIER',
    checkStatsigFeatureGate_CACHED_MAY_BE_STALE:()=>false,shouldAllowManagedPermissionRulesOnly:()=>false,getEnabledSettingSources:()=>[],
    logForDebugging:()=>{},jsonStringify:JSON.stringify,
  }
  process.env.USER_TYPE='external'
  delete process.env.CLAUDE_CODE_REMOTE
  if(row.pwd)process.env.PWD=row.pwd;else delete process.env.PWD
  // Real frozen initializeToolPermissionContext and actual helpers execute.
  // Settings/empty rule sources and mapped native filesystem are the inputs;
  // no output, directory validation, map update or result is substituted.
  const run=new Function('dependencies',`with(dependencies){${code}\nreturn initializeToolPermissionContext}`)(dependencies)
  try {
    const result=await run({allowedToolsCli:[],disallowedToolsCli:[],permissionMode:'default',allowDangerouslySkipPermissions:false,addDirs:row.addDirs??[]})
    rows.push({directories:Array.from(result.toolPermissionContext.additionalWorkingDirectories.entries()),warnings:result.warnings,accessed})
  }catch(error:any){rows.push({error:error.code??error.message,accessed})}
}
process.stdout.write(JSON.stringify({rows}))
