import {expandGlobPattern} from '@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-utils.js'
const input=JSON.parse(await Bun.stdin.text())
process.chdir(input.cwd)
process.stdout.write(JSON.stringify(input.patterns.map((p:string)=>expandGlobPattern(p)))+'\n')
