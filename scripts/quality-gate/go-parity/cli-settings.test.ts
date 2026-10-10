import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { cliSettingsProfiles, type SettingsProfile } from './cli-settings-fixtures'
const root = resolve(import.meta.dir, '../../..')
const sourceOnly = process.env.CC_HAHA_CLI_SETTINGS_SOURCE_ONLY === '1'
let buildHome: string, executable: string
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('CLI settings proof requires Bun1.3.14')
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(),'cli-settings-build-'))
  executable = join(buildHome,'settings.test')
  const child = Bun.spawn(['go','test','-c','-o',executable,'./internal/clisettings'],{cwd:join(root,'backend'),env:await createGoBuildEnvironment(root,buildHome),stdin:'ignore',stdout:'pipe',stderr:'pipe'})
  const [code,out,err] = await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
  expect(code,out+err).toBe(0)
},180000)
afterAll(async()=>{if(buildHome)await rm(buildHome,{recursive:true,force:true})})
export async function recordCliSettings(source: boolean, profile: SettingsProfile) {
  const home = await mkdtemp(join(tmpdir(),'cli-settings-parity-'))
  try {
    const env = createSandboxedTestEnvironment(home,{CC_HAHA_FIXTURE_TEMP_ROOT:tmpdir(),NODE_ENV:'production',USER_TYPE:'external',CLAUDE_CODE_ENABLE_XAA:'0',CLAUDE_CODE_SIMPLE:'1',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC:'1',DISABLE_TELEMETRY:'1',ANTHROPIC_API_KEY:'fixture-not-a-secret',CC_HAHA_CLI_SETTINGS_FIXTURE:'1'})
    const command = source ? [process.execPath,'--no-env-file','--feature=TRANSCRIPT_CLASSIFIER',join(root,'scripts/quality-gate/go-parity/cli-settings.fixture.ts')] : [executable,'-test.run=^TestCLISettingsFixture$']
    const child = Bun.spawn(command,{cwd:home,env,stdin:new Blob([JSON.stringify({...profile,home})]),stdout:'pipe',stderr:'pipe'})
    const timer = setTimeout(()=>child.kill(),20000)
    try {
      const [code,out,err] = await Promise.all([child.exited,new Response(child.stdout).arrayBuffer(),new Response(child.stderr).arrayBuffer()])
      const stdout = Buffer.from(out).toString('utf8'), stderr = Buffer.from(err).toString('utf8')
      const directory = join(root,'artifacts/go-parity/cli-settings-raw')
      await mkdir(directory,{recursive:true})
      await writeFile(join(directory,basename(home)+'-'+(source?'source':'go')+'.json'),JSON.stringify({profile:profile.name,home,command,env,code,stdoutBase64:Buffer.from(out).toString('base64'),stderrBase64:Buffer.from(err).toString('base64')},null,2)+'\n')
      expect(code,stdout+stderr).toBe(0)
      expect(stderr).toBe('')
      const value = JSON.parse(stdout.replace(/\nPASS\n$/,'').replaceAll(home,'<HOME>'))
      if(source){expect(value.capabilities.blocked).toEqual([]);expect(value.capabilities.secureReads).toEqual([])}
      // Native binary transport metadata is not semantic; returned bytes/order are.
      return {name:value.name,results:value.results}
    } finally {clearTimeout(timer)}
  } finally {await rm(home,{recursive:true,force:true})}
}
for(const profile of cliSettingsProfiles){
  test('CLI settings actual Source self: '+profile.name,async()=>{expect(await recordCliSettings(true,profile)).toEqual(await recordCliSettings(true,profile))},60000)
  ;(sourceOnly?test.skip:test)('CLI settings independent Go: '+profile.name,async()=>{const source=await recordCliSettings(true,profile);expect(await recordCliSettings(false,profile)).toEqual(source)},60000)
}
test.each(['file bytes','mark order','reset failure','cache stale','undefined','UTF16'])('settings comparator rejects %s',async mutation=>{
  const name = mutation==='reset failure'?'failure-mark-retained':mutation==='cache stale'?'path-cache-clone-stale-writer':mutation==='undefined'?'catch-own-undefined':'env-order-numeric-utf16'
  const expected=await recordCliSettings(true,cliSettingsProfiles.find(p=>p.name===name)!)
  const changed=structuredClone(expected)
  if(mutation==='file bytes')changed.results.at(-1)!.file.rawBase64+='AA=='
  else if(mutation==='mark order')changed.results[1].effects.reverse()
  else if(mutation==='reset failure')changed.results[1].effects.push({kind:'reset'})
  else if(mutation==='cache stale')changed.results.at(-1)!.observation.value.source='{"model":"external"}'
  else if(mutation==='undefined')changed.results[0].observation.value.fields=changed.results[0].observation.value.fields.filter((f:any)=>f.kind!=='undefined')
  else changed.results[0].observation.value.source=changed.results[0].observation.value.source.replace('\\ud800','�')
  expect(()=>expect(changed).toEqual(expected)).toThrow()
})
test.each(['home','config','extra-input'])('settings fixture guard before imports %s',async fault=>{
  const home=await mkdtemp(join(tmpdir(),'cli-settings-guard-'))
  try{
    const loader=join(home,'guard.ts')
    await writeFile(loader,`import {plugin} from 'bun'\nplugin({name:'tripwire',setup(build){build.onLoad({filter:/[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/},()=>{process.stdout.write('PRODUCTION_REACHED');return {contents:'throw new Error("production reached")',loader:'ts'}})}})\nawait import(${JSON.stringify(join(root,'scripts/quality-gate/go-parity/cli-settings.fixture.ts'))})`)
    const env=createSandboxedTestEnvironment(home,{CC_HAHA_FIXTURE_TEMP_ROOT:tmpdir(),CC_HAHA_CLI_SETTINGS_FIXTURE:'1',NODE_ENV:'production',USER_TYPE:'external',CLAUDE_CODE_ENABLE_XAA:'0',...(fault==='home'?{HOME:home+'/wrong'}:{}),...(fault==='config'?{CLAUDE_CONFIG_DIR:home+'/wrong'}:{})})
    const child=Bun.spawn([process.execPath,'--no-env-file','--feature=TRANSCRIPT_CLASSIFIER',loader],{cwd:home,env,stdin:new Blob([JSON.stringify({home,name:'guard',operations:[],...(fault==='extra-input'?{extra:true}:{})})]),stdout:'pipe',stderr:'pipe'})
    const [code,out,err]=await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
    expect(code).not.toBe(0);expect(out).not.toContain('PRODUCTION_REACHED');expect(err).toContain(fault==='extra-input'?'declared private fixture':'disposable HOME/config before production imports')
  }finally{await rm(home,{recursive:true,force:true})}
})
if(process.env.CC_HAHA_CAPTURE_CLI_SETTINGS==='1'){
  const rows=[]
  for(const input of cliSettingsProfiles)rows.push({input,expected:await recordCliSettings(true,input)})
  await writeFile(join(root,'backend/internal/clisettings/testdata/parser_writer_contract.json'),JSON.stringify(rows,null,2)+'\n')
}
