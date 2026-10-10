import {wrapCommandWithSandboxLinux} from '@anthropic-ai/sandbox-runtime/dist/sandbox/linux-sandbox-utils.js'
const input=JSON.parse(await Bun.stdin.text())
process.chdir(input.cwd)
// The platform primitive is Linux for this Linux producer differential.
Object.defineProperty(process,'platform',{value:'linux'})
const output=[]
for(const row of input.rows){
 try {output.push({result:await wrapCommandWithSandboxLinux({...row,readConfig:row.read,writeConfig:row.write,ripgrepConfig:{command:input.rg},mandatoryDenySearchDepth:3})})}
 catch(e:any){output.push({error:e.message})}
}
process.stdout.write(JSON.stringify(output)+'\n')
