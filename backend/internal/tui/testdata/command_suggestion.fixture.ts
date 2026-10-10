import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Command suggestion')
if(process.env.CC_HAHA_TUI_COMMAND_SUGGESTION_FIXTURE!=='1')throw new Error('Unowned command suggestion fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const {applyCommandSuggestion}=await import('../../../../src/utils/suggestions/commandSuggestions')
const {parseArguments,generateProgressiveArgumentHint}=await import('../../../../src/utils/argumentSubstitution')
const rows=input.rows.map((row:any)=>{
 const inputs:string[]=[];const cursors:number[]=[];const submitted:any[]=[]
 const command={type:row.kind,name:row.name,description:'Actual choice metadata',argNames:row.args}
 applyCommandSuggestion({id:'owned-choice',displayText:row.name,metadata:command} as any,row.execute,[command] as any,value=>inputs.push(value),value=>cursors.push(value),(text,slash)=>submitted.push({text,slash}))
 return{inputs,cursors,submitted}
})
if(capabilities.blocked.length)throw new Error('Blocked command fixture:'+capabilities.blocked.join(', '))
const hints=(input.hints??[]).map((args:string)=>generateProgressiveArgumentHint(['target','region','extra'],parseArguments(args))??null)
process.stdout.write(JSON.stringify({rows,hints,capabilities:{blocked:capabilities.blocked}})+'\n')
