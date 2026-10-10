import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Bash Git rules')
if(process.env.CC_HAHA_BASH_GIT_RULE_FIXTURE!=='1')throw new Error('Unowned Bash Git rules fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home)
const {getEmptyToolPermissionContext}=await import('../../../../src/Tool')
const {bashToolCheckPermission}=await import('../../../../src/tools/BashTool/bashPermissions')
const rows=input.rows.map((row:any)=>{const context={...getEmptyToolPermissionContext(),...row.context};return bashToolCheckPermission({command:row.command},context).behavior})
if(capabilities.blocked.length)throw new Error('Blocked original Bash rules: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
