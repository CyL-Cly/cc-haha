import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME,'Actual task budget')
if(process.env.CC_HAHA_TASK_BUDGET_FIXTURE!=='1')throw new Error('Unowned task budget fixture')
const input=await Bun.stdin.json()
const capabilities=await installClientFixtureCapabilities(process.env.HOME!,{absentPaths:absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO={VERSION:'source-fixture',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
const {configureTaskBudgetParams}=await import('../../src/services/api/claude')
const {finalContextTokensFromLastResponse}=await import('../../src/utils/tokens')
const rows=[]
const names=['ANTHROPIC_BASE_URL','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_USE_AZURE_OPENAI','CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS','USER_TYPE']
for(const row of input.rows??[]){
  for(const name of names)delete process.env[name]
  for(const [name,value]of Object.entries(row.env??{})){if(!names.includes(name)||typeof value!=='string')throw new Error('Unsupported provider environment');process.env[name]=value}
  const output={...(row.output??{})},betas=[...(row.betas??[])]
  configureTaskBudgetParams(row.budget,output,betas)
  rows.push({output,betas})
}
const windows=[]
for(const history of input.histories??[]){try{windows.push({value:finalContextTokensFromLastResponse(history)})}catch(error){windows.push({error:(error as Error).message})}}
process.stdout.write(JSON.stringify({rows,windows,capabilities}))
