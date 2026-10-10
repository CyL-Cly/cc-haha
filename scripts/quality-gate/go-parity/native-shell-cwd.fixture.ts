import { parse } from 'acorn'
import { readFileSync, unlinkSync, writeFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original Shell cwd result consumer')
if (process.env.CC_HAHA_SHELL_CWD_FIXTURE !== '1') throw new Error('Unowned Shell cwd fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/Shell.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const find = (node: any): any => {
 if (!node || typeof node !== 'object') return
 if (node.type === 'CallExpression' && node.callee?.type === 'MemberExpression' && node.callee.property?.name === 'then' && node.callee.object?.type === 'MemberExpression' && node.callee.object.property?.name === 'result' && node.callee.object.object?.name === 'shellCommand') return node.arguments[0]
 for (const key of Object.keys(node)) { const child = node[key]; if (Array.isArray(child)) { for (const n of child) { const value = find(n); if (value) return value } } else { const value = find(child); if (value) return value } }
}
const callback = find(ast)
if (!callback) throw new Error('Missing Source result.then cwd consumer')
const implementation = new Function('dependencies', `with(dependencies){return (${source.slice(callback.start, callback.end)})}`)
const rows = []
for (let i = 0; i < input.rows.length; i++) {
 const row = input.rows[i], path = join(input.homeDir, 'cwd-' + i)
 if (row.content !== null) writeFileSync(path, row.content)
 const changed: any[] = [], hooks: any[] = []
 let invalidated = 0
 const consume = implementation({ shouldUseSandbox: false, preventCwdChanges: row.prevent, cwd: row.cwd, nativeCwdFilePath: path, readFileSync, unlinkSync, getPlatform: () => 'darwin', setCwd: (...args: any[]) => changed.push(args), invalidateSessionEnvCache: () => invalidated++, onCwdChangedForHooks: (...args: any[]) => { hooks.push(args); return Promise.resolve() }, logEvent() {} })
 await consume(row.result)
 rows.push({ changed, hooks, invalidated, exists: existsSync(path) })
}
process.stdout.write(JSON.stringify({ rows }))
