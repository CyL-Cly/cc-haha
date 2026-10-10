import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Release notes')
if(process.env.CC_HAHA_RELEASE_NOTES_FIXTURE!=='1')throw new Error('Unowned release notes fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const source=await import('../../../../src/utils/releaseNotes')
const output=input.contents.map((content:string)=>({parsed:source.parseChangelog(content),all:source.getAllReleaseNotes(content)}))
const orders=input.pairs.map(([a,b]:[string,string])=>{try{return Bun.semver.order(a,b)}catch{return null}})
if(capabilities.blocked.length)throw new Error('Blocked original release notes: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({output,orders})+'\n')
