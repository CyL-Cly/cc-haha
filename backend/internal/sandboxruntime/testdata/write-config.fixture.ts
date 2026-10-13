import fs from 'node:fs'
import { parse } from 'acorn'
import * as helpers from '@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-utils.js'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = JSON.parse(await Bun.stdin.text())
assertDisposableFixtureHome(input.home, 'Sandbox write-config Source')
const code = fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-manager.js', import.meta.url), 'utf8')
const ast: any = parse(code, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const declaration = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'getFsWriteConfig')
if (!declaration) throw new Error('Actual sandbox-runtime write getter missing')
const rows = input.rows.map((row: any) => {
  // Only immutable module config and OS discovery are supplied seams. The
  // actual frozen dependency getter and default/path helpers execute intact.
  const dependencies = { ...helpers, config: row.config, getPlatform: () => row.platform, logForDebugging: () => {} }
  const getter = new Function('dependencies', `with(dependencies){${code.slice(declaration.start, declaration.end)};return getFsWriteConfig}`)(dependencies)
  return getter()
})
process.stdout.write(JSON.stringify(rows) + '\n')
