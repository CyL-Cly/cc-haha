import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
import {join} from 'node:path'
import {mkdir,writeFile} from 'node:fs/promises'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Builtin init')
if(process.env.CC_HAHA_BUILTIN_INIT_FIXTURE!=='1')throw new Error('Unowned init fixture')
const ancestors=[]
for(let current=join(input.cwd??home,'..');;current=join(current,'..')){if(!current.startsWith(home+'/'))ancestors.push(join(current,'.git'));if(current===join(current,'..'))break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),...ancestors]})
if(input.project){
 await mkdir(process.env.CLAUDE_CONFIG_DIR??home,{recursive:true})
 await writeFile(join(process.env.CLAUDE_CONFIG_DIR??home,'.claude.json'),JSON.stringify({hasCompletedOnboarding:true,futureGlobal:'preserved',projects:{[input.cwd??home]:input.project}}))
}
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state')
state.setCwdState(input.cwd??home);state.setOriginalCwd(input.cwd??home);state.setProjectRoot(input.cwd??home)
const config=await import('../../../../src/utils/config');config.enableConfigs()
const {default:command}=await import('../../../../src/commands/init')
const rows=[]
for(const row of input.rows){
 process.env.USER_TYPE=row.userType;process.env.CLAUDE_CODE_NEW_INIT=row.flag
 rows.push({metadata:JSON.parse(JSON.stringify(command)),prompt:await command.getPromptForCommand(row.args,{} as any)})
}
if(capabilities.blocked.length)throw new Error('Blocked original init:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,project:config.getCurrentProjectConfig()})+'\n')
