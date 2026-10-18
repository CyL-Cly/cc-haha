import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual session title text')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_SESSION_TITLE_TEXT_SOURCE!=='1')throw new Error('Owned pinned title fixture required')
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const text=await import('../../../src/utils/sessionTitleText')
const title=await import('../../../src/utils/sessionTitle')
const {truncateToWidth}=await import('../../../src/utils/truncate')
const rows=input.values.map((value:string)=>({input:value,clean:text.cleanSessionTitleSource(value),normalized:title.normalizeGeneratedSessionTitle(value),truncated:truncateToWidth(value,75)}))
if(caps.blocked.length)throw new Error('Actual title text escaped private capabilities '+JSON.stringify(caps.blocked))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({rows,blocked:caps.blocked})+'\n',error=>error?reject(error):resolve()))
process.exit(0)
