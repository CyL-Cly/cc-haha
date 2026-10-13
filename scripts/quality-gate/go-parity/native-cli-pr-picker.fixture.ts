import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original PR session picker')
if (process.env.CC_HAHA_CLI_PR_PICKER_FIXTURE !== '1') throw new Error('Unowned PR picker fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key'] })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const source = new Bun.Transpiler({ loader: 'tsx' }).transformSync(await Bun.file(new URL('../../../src/screens/ResumeConversation.tsx', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const parsePR = ast.body.find((n: any) => n.type === 'FunctionDeclaration' && n.id.name === 'parsePrIdentifier')
let filterCallback: any
function walk(node: any) {
 if (!node || typeof node !== 'object') return
 if (node.type === 'VariableDeclarator' && node.id.name === 'filteredLogs' && node.init.type === 'CallExpression') filterCallback = node.init.arguments[0]
 for (const value of Object.values(node)) if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value)
}
walk(ast)
if (!parsePR || !filterCallback) throw new Error('Original PR parser/filter missing')
const filter = new Function('logs', 'filterByPr', `${source.slice(parsePR.start, parsePR.end)};return (${source.slice(filterCallback.start, filterCallback.end)})()`)
const rows = []
for (const row of input.rows) {
 state.setOriginalCwd(row.cwd)
 state.setCwdState(row.cwd)
 const result = row.allProjects ? await storage.loadAllProjectsMessageLogsProgressive(undefined, row.count ?? 50) : await storage.loadSameRepoMessageLogsProgressive(row.worktrees ?? [], undefined, row.count ?? 50)
 const normalized = result.logs.map((log: any) => ({ ...log, created: log.created.getTime(), modified: log.modified.getTime() }))
 rows.push({ logs: filter(normalized, row.filter), nextIndex: result.nextIndex, total: result.allStatLogs.length })
}
process.stdout.write(JSON.stringify({ rows, blocked: capabilities.blocked }))
