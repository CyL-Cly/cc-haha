import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
if (process.env.CC_HAHA_READ_SAFETY_FIXTURE !== '1') throw new Error('private only')
const root = resolve(import.meta.dir, '../..')
function actualFunction(path: string, name: string) {
 const source = readFileSync(root+'/src/'+path, 'utf8')
 const start = source.indexOf('function '+name+'(')
 const end = source.indexOf('\n}\n', start)+3
 if (start<0 || end<3) throw new Error('actual Source function changed')
 return source.slice(start,end)
}
const actual = actualFunction('utils/shell/readOnlyCommandValidation.ts','containsVulnerableUncPath')+'\n'+actualFunction('utils/permissions/filesystem.ts','hasSuspiciousWindowsPathPattern')
const code = new Bun.Transpiler({loader:'ts'}).transformSync(actual)
const run = new Function('getPlatform','path','options',code+'\nreturn hasSuspiciousWindowsPathPattern(path,options)')
console.log(JSON.stringify((await Bun.stdin.json()).map((row: any) => run(() => row.platform,row.path,{allowUncPath:row.allowUNC}))))
