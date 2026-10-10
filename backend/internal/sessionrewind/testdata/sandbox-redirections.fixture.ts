import {extractOutputRedirections} from '../../../../src/utils/bash/commands'
const input=JSON.parse(await Bun.stdin.text())
const rows=[]
for(const command of input.commands){rows.push(extractOutputRedirections(command))}
process.stdout.write(JSON.stringify(rows)+'\n')
