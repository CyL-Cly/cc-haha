import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Computer Use abort cleanup')
if (process.env.CC_HAHA_CU_ABORT_CLEANUP_FIXTURE !== '1') throw new Error('Unowned CU cleanup fixture')
await import('../../../preload')
const { parse } = await import('acorn')
const { cleanupComputerUseAfterTurn } = await import('../../../src/utils/computerUse/cleanup')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/query.ts', import.meta.url)).text())
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const branches: any[] = []
const walk = (node: any) => {
  if (!node || typeof node !== 'object') return
  if (node.type === 'IfStatement' && node.test?.type === 'UnaryExpression' && node.test.operator === '!' && node.test.argument?.property?.name === 'agentId' && source.slice(node.start, node.end).includes('cleanupComputerUseAfterTurn')) branches.push(node)
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') walk(value)
  }
}
walk(ast)
if (branches.length !== 2) throw new Error('Missing actual streaming/tool abort cleanup branches')
const rows = []
for (const branch of branches) {
  const imports: any[] = []
  const importsIn = (node: any) => {
    if (!node || typeof node !== 'object') return
    if (node.type === 'ImportExpression') imports.push(node)
    for (const value of Object.values(node)) {
      if (Array.isArray(value)) value.forEach(importsIn)
      else if (value && typeof value === 'object') importsIn(value)
    }
  }
  importsIn(branch)
  if (imports.length !== 1) throw new Error('Unexpected abort import dependency')
  // Only the import dependency is injected. The actual two query.ts branches
  // and the imported cleanup function execute unchanged.
  const dependency = imports[0]
  const body = source.slice(branch.start, dependency.start) + 'importCleanup()' + source.slice(dependency.end, branch.end)
  const execute = new Function('toolUseContext', 'importCleanup', `return (async()=>{${body}})()`)
  for (const profile of input.profiles) {
    const controller = new AbortController()
    if (profile.cancelled) controller.abort()
    let calls = 0
    const phases: string[] = []
    const context = { agentId: profile.child ? 'child-agent' : undefined, abortController: controller, getAppState: () => { phases.push('state'); return {} }, setAppState: () => { throw new Error('No state write without hidden apps') } }
    await execute(context, async () => ({ cleanupComputerUseAfterTurn: async (original: any) => {
      if (original !== context) throw new Error('Original context lost')
      calls++
      await cleanupComputerUseAfterTurn(original, {
        hideCursorBadge: () => { phases.push('badge'); if (profile.badgeFailure) throw new Error('Badge failure') },
        overlayHide: async () => { phases.push('overlay'); if (profile.overlayFailure) throw new Error('Overlay failure') },
      })
    } }))
    rows.push({ calls, phases, cancelled: controller.signal.aborted })
  }
}
process.stdout.write(JSON.stringify({ branches: branches.length, rows }))
