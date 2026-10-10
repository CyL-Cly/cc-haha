import { readFileSync } from 'node:fs'
import { resolve, posix } from 'node:path'
if (process.env.CC_HAHA_WORKING_PATH_FIXTURE !== '1') throw new Error('fixture only')
const file = readFileSync(resolve(import.meta.dir, '../../../src/utils/permissions/filesystem.ts'),'utf8')
const paths = readFileSync(resolve(import.meta.dir, '../../../src/utils/path.ts'),'utf8')
function segment(source: string, start: string, end: string) {
 const i=source.indexOf(start), j=source.indexOf(end,i)
 if(i<0||j<0) throw new Error('Source function anchors missing')
 return source.slice(i,j).replaceAll('export function','function')
}
const actual = segment(file,'export function normalizeCaseForComparison(', '\n/**') + segment(file,'export function relativePath(', '\n/**') + segment(file,'export function pathInWorkingPath(', '\nfunction rootPathForSource') + segment(paths,'export function containsPathTraversal(', '\n// Re-export')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(actual)
const run=new Function('rows','posix', 'const expandPath=s=>posix.resolve(s); const getPlatform=()=>"linux";\n'+code+'\nreturn rows.map(row=>pathInWorkingPath(row.path,row.root));')
console.log(JSON.stringify(run(await Bun.stdin.json(),posix)))
