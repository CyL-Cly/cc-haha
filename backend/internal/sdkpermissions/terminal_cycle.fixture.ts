import {assertDisposableFixtureHome} from '../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../scripts/quality-gate/go-parity/client-options-capabilities'
import {mkdir,writeFile} from 'node:fs/promises'
import {join} from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Terminal permission cycle')
if(process.env.CC_HAHA_TERMINAL_CYCLE_FIXTURE!=='1')throw new Error('Unowned terminal mode fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
await mkdir(join(home,'.claude'),{recursive:true})
// The active classifier is an independent, explicitly unported leaf. Exercise
// the real live gate's inactive profile, with and without its compiled feature.
await writeFile(join(home,'.claude/settings.json'),JSON.stringify({disableAutoMode:'disable',future:{preserved:true}}))
const state=await import('../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {getNextPermissionMode}=await import('../../../src/utils/permissions/getNextPermissionMode')
const rows=input.rows.map((row:any)=>{
 process.env.USER_TYPE=row.UserType
 return getNextPermissionMode({mode:row.Mode,isBypassPermissionsModeAvailable:row.Bypass,isAutoModeAvailable:row.Auto} as any)
})
if(capabilities.blocked.length)throw new Error('Blocked cycle fixture:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
