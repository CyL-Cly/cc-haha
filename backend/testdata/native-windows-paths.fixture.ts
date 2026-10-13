import { readFileSync } from 'node:fs'
import { resolve, win32, posix } from 'node:path'

if (process.env.CC_HAHA_WINDOWS_PATH_FIXTURE !== '1') throw new Error('private fixture only')
const root = resolve(import.meta.dir, '../..')
const rows = await Bun.stdin.json() as { value: string; cwd: string; home: string; target?: string; join?: string[] }[]
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
const actual = windowsSource.slice(start, end).replaceAll('export const ', 'const ') + '\n' +
  actualFunction('utils/path.ts', 'expandPath') + '\n' +
  actualFunction('utils/path.ts', 'containsPathTraversal') + '\n' +
  actualFunction('utils/permissions/filesystem.ts', 'relativePath') + '\n' +
  actualFunction('utils/permissions/filesystem.ts', 'normalizeCaseForComparison') + '\n' +
  actualFunction('utils/permissions/filesystem.ts', 'pathInWorkingPath')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(actual)
const run = new Function('memoizeWithLRU', 'getPlatform', 'getCwd', 'homedir', 'getFsImplementation', 'normalize', 'isAbsolute', 'join', 'resolve', 'posix', 'row', code + `
  return {
    posix: windowsPathToPosixPath(row.value), windows: posixPathToWindowsPath(row.value),
    clean: normalize(row.value), dir: row.win32.dirname(row.value), resolved:row.win32.resolve(row.cwd,row.value),
    join: row.join ? join(...row.join) : '',
    expanded: expandPath(row.value, row.cwd),
    relative: row.target ? relativePath(row.value, row.target) : '',
    within: row.target ? pathInWorkingPath(row.target, row.value) : false,
  }
`)
console.log(JSON.stringify(rows.map(row => run(
  (fn: unknown) => fn, () => 'windows', () => row.cwd, () => row.home,
  () => ({ cwd: () => row.cwd }), win32.normalize, win32.isAbsolute,
  win32.join, win32.resolve, posix, { ...row, win32 },
))))
