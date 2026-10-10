import { assertDisposableFixtureHome } from './fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual teammate hard fallback models')
if(process.env.CC_HAHA_TEAM_FALLBACK_FIXTURE!=='1')throw new Error('Missing fallback fixture')
await import('../../../preload')
const {getHardcodedTeammateModelFallback}=await import('../../../src/utils/swarm/teammateModel')
const keys=['ANTHROPIC_DEFAULT_OPUS_MODEL','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_USE_AZURE_OPENAI']
const rows=[]
for(const row of input.rows){for(const key of keys)delete process.env[key];Object.assign(process.env,row);rows.push(getHardcodedTeammateModelFallback())}
process.stdout.write(JSON.stringify({rows}))
