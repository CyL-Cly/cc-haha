import { readFileSync } from 'node:fs'
import { resolve, win32, posix } from 'node:path'
import ignore from '../../node_modules/ignore/index.js'

if (process.env.CC_HAHA_WINDOWS_PERMISSION_FIXTURE !== '1') throw new Error('private fixture only')
const root = resolve(import.meta.dir, '../..')
const rows = await Bun.stdin.json() as any[]
function actualFunction(file: string, name: string) {
  const source = readFileSync(`${root}/src/${file}`, 'utf8')
  const start = source.indexOf(`function ${name}(`)
  const end = source.indexOf('\n}\n', start) + 3
  if (start < 0 || end < 3) throw new Error(`actual Source changed: ${name}`)
  return source.slice(start, end)
}
const windowsSource = readFileSync(`${root}/src/utils/windowsPaths.ts`, 'utf8')
const start = windowsSource.indexOf('export const windowsPathToPosixPath =')
const end = windowsSource.indexOf('\n)', windowsSource.indexOf('export const posixPathToWindowsPath =', start)) + 3
if (start < 0 || end < 3) throw new Error('actual Windows path exports changed')
const filesystem = ['relativePath','rootPathForSource','patternWithRoot','getPatternsByRoot','matchingRuleForInput']
const parser = ['unescapeRuleContent','permissionRuleValueFromString','findFirstUnescapedChar','findLastUnescapedChar']
const permissions = readFileSync(`${root}/src/utils/permissions/permissions.ts`,'utf8')
const sourcesStart = permissions.indexOf('const PERMISSION_RULE_SOURCES =')
const sourcesEnd = permissions.indexOf('\n\n', sourcesStart)
const actual = windowsSource.slice(start,end).replaceAll('export const ','const ') + '\n' +
  actualFunction('utils/path.ts','expandPath') + '\n' +
  filesystem.map(name => actualFunction('utils/permissions/filesystem.ts',name)).join('\n') + '\n' +
  parser.map(name => actualFunction('utils/permissions/permissionRuleParser.ts',name)).join('\n') + '\n' +
  permissions.slice(sourcesStart,sourcesEnd) + '\n' +
  actualFunction('utils/permissions/permissions.ts','getDenyRules') + '\n' +
  actualFunction('utils/permissions/permissions.ts','getRuleByContentsForToolName')
const code = new Bun.Transpiler({loader:'ts'}).transformSync(actual)
const run = new Function('memoizeWithLRU','getPlatform','getCwd','getOriginalCwd','homedir','getFsImplementation','normalize','isAbsolute','join','resolve','posix','ignore','getSettingsRootPathForSource','normalizeLegacyToolName','SETTING_SOURCES','row',`const DIR_SEP='/',FILE_EDIT_TOOL_NAME='Edit',FILE_READ_TOOL_NAME='Read';\n`+code+`
 const matched = matchingRuleForInput(row.path,{alwaysDenyRules:row.rules},row.tool === 'Read' ? 'read' : 'edit','deny')
 return matched ? { pattern:matched.ruleValue.ruleContent,source:matched.source } : null
`)
console.log(JSON.stringify(rows.map(row => run(
  (fn: unknown) => fn, () => 'windows', () => row.cwd, () => row.cwd, () => row.home,
  () => ({cwd:()=>row.cwd}),win32.normalize,win32.isAbsolute,win32.join,win32.resolve,posix,ignore,
  (source:string)=>row.roots[source],(name:string)=>name,
  ['userSettings','projectSettings','localSettings','flagSettings','policySettings'],row,
))))
