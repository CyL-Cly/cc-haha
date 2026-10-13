import { readFileSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { resolve, join } from 'node:path'
import mapValues from 'lodash-es/mapValues.js'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Public MCP CLI inputs')
if (process.env.CC_HAHA_CLI_MCP_INPUT_FIXTURE !== '1') throw new Error('Unowned CLI MCP inputs')
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
process.env.USER_TYPE='ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH=join(home,'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
const managed=getManagedFilePath()
if(managed!==join(home,'managed'))throw new Error('Unexpected managed root')
process.env.USER_TYPE='external'
const state=await import('../../../src/bootstrap/state')
state.setOriginalCwd(home);state.setProjectRoot(home);state.setCwdState(home)
state.setAllowedSettingSources(['userSettings','projectSettings','localSettings','policySettings'])
const { enableConfigs }=await import('../../../src/utils/config');enableConfigs()
const cache=await import('../../../src/utils/settings/settingsCache')
const { safeParseJSON }=await import('../../../src/utils/json')
const api=await import('../../../src/services/mcp/config')
const chrome=await import('../../../src/utils/claudeInChrome/common')
const computerUse=await import('../../../src/utils/computerUse/common')
const { plural }=await import('../../../src/utils/stringUtils')
const text=readFileSync(new URL('../../../src/main.tsx',import.meta.url),'utf8')
const start=text.indexOf('let dynamicMcpConfig:')
const end=text.indexOf('// Extract Claude in Chrome option',start)
const strictStart=text.indexOf('const strictMcpConfig = options.strictMcpConfig')
const strictEnd=text.indexOf('// chicago MCP:',strictStart)
if(start<0||end<0||strictStart<0||strictEnd<0)throw new Error('Actual MCP startup blocks missing')
const body=new Bun.Transpiler({loader:'tsx'}).transformSync(text.slice(start,end)+'\n'+text.slice(strictStart,strictEnd)).replace("await import(\"src/utils/computerUse/common.js\")",'computerUse')
const AsyncFunction=Object.getPrototypeOf(async function(){}).constructor
const run=new AsyncFunction('deps',`with(deps){${body};return dynamicMcpConfig}`)
mkdirSync(join(home,'.claude'),{recursive:true});mkdirSync(managed,{recursive:true})
const observations=[]
for(const row of input.rows){
 writeFileSync(join(home,'.claude','settings.json'),JSON.stringify(row.settings??{}))
 writeFileSync(join(managed,'managed-settings.json'),JSON.stringify(row.policy??{}))
 const enterprise=join(managed,'managed-mcp.json');rmSync(enterprise,{force:true})
 if(row.enterprise!==undefined)writeFileSync(enterprise,JSON.stringify(row.enterprise))
 for(const [name,value] of Object.entries(row.files??{}))writeFileSync(join(home,name),value as string)
 cache.resetSettingsCache();api.doesEnterpriseMcpConfigExist.cache.clear()
 const output:string[]=[]
 let error:string|undefined,servers:unknown
 try{servers=await run({mcpConfig:row.inputs,options:{strictMcpConfig:row.strict},safeParseJSON,parseMcpConfig:api.parseMcpConfig,parseMcpConfigFromFilePath:api.parseMcpConfigFromFilePath,resolve:(value:string)=>resolve(home,value),logForDebugging:()=>{},process:{stderr:{write:(text:string)=>output.push(text)},exit:()=>{throw new Error('exit')}},mapValues,filterMcpServersByPolicy:api.filterMcpServersByPolicy,plural,isClaudeInChromeMCPServer:chrome.isClaudeInChromeMCPServer,CLAUDE_IN_CHROME_MCP_SERVER_NAME:chrome.CLAUDE_IN_CHROME_MCP_SERVER_NAME,computerUse,doesEnterpriseMcpConfigExist:api.doesEnterpriseMcpConfigExist,areMcpConfigsAllowedWithEnterpriseMcpConfig:api.areMcpConfigsAllowedWithEnterpriseMcpConfig,chalk:{red:(text:string)=>text}})}catch(e){error=output.pop()??String(e)}
 observations.push({servers:servers??null,warnings:output.map(text=>text.replace(/\n$/,'')),error:error??null})
}
process.stdout.write(JSON.stringify(observations))
