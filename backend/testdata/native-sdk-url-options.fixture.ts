import fs from 'node:fs'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Actual SDK URL option consumers')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned SDK URL fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../preload')
const main = fs.readFileSync(new URL('../../src/main.tsx', import.meta.url), 'utf8')
const start = main.indexOf('    // Auto-set input/output formats, verbose mode, and print mode when SDK URL is provided')
const end = main.indexOf('    // Extract teleport option', start)
const validateStart = main.indexOf("    if (inputFormat === 'stream-json' && outputFormat !== 'stream-json')")
const validateEnd = main.indexOf('    // Validate replayUserMessages', validateStart)
if ([start, end, validateStart, validateEnd].some(index => index < 0)) throw new Error('Original SDK URL argument consumer missing')
const execute = new Function('options', new Bun.Transpiler({ loader: 'ts' }).transformSync('let {sdkUrl,inputFormat,outputFormat,verbose,print}=options;let error;const console={error:(value)=>error=value};const process={exit:()=>{throw new Error(error)}};try{'+main.slice(start,end)+main.slice(validateStart,validateEnd)+'}catch(error){return {error:error.message}};return {inputFormat,outputFormat,verbose:!!verbose,print:!!print}'))
const printSource = fs.readFileSync(new URL('../../src/cli/print.ts', import.meta.url),'utf8')
const promptStart = printSource.indexOf('  let inputStream: AsyncIterable<string>',printSource.indexOf('function getStructuredIO('))
const promptEnd = printSource.indexOf('  // Use RemoteIO if sdkUrl is provided',promptStart)
if(promptStart<0||promptEnd<0)throw new Error('Original RemoteIO initial prompt consumer missing')
const {fromArray}=await import('../../src/utils/generators')
const {jsonStringify}=await import('../../src/utils/slowOperations')
const makeInput=new Function('inputPrompt','fromArray','jsonStringify',new Bun.Transpiler({loader:'ts'}).transformSync(printSource.slice(promptStart,promptEnd)+'\nreturn inputStream'))
const rows=[]
for(const row of input.rows){const result=execute(row.options);const initial=[];for await(const line of makeInput(row.prompt??'',fromArray,jsonStringify))initial.push(line);rows.push({...result,initial})}
if(capabilities.blocked.length)throw new Error('Original SDK URL capability escape: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,capabilities}))
