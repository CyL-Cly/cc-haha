import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original plugin and workflow terminal consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_WORKFLOW_SOURCE !== '1') throw new Error('Missing Source consumer guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function originalFunction(path: string, name: string, dependencies: Record<string, unknown> = {}) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const declaration = source.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === name)
  if (!declaration) throw new Error('Original function identity changed: ' + name)
  const body = new Bun.Transpiler({ loader: 'tsx' }).transformSync(declaration.getText(source)).replace('export ', '')
  return new Function(...Object.keys(dependencies), body + '\nreturn ' + name)(...Object.values(dependencies))
}
const parse = originalFunction('src/commands/plugin/parseArgs.ts', 'parsePluginArgs')
const initial = originalFunction('src/commands/plugin/PluginSettings.tsx', 'getInitialViewState')
const groups = originalFunction('src/components/tasks/WorkflowDetailDialog.tsx', 'groupProgress')
const filter = originalFunction('src/components/tasks/WorkflowDetailDialog.tsx', 'applyFilter')
const final = originalFunction('src/commands/plugin/PluginOptionsDialog.tsx', 'buildFinalValues', {
  isEnvTruthy: (value: string | undefined) => !!value && ['1', 'true', 'yes', 'on'].includes(value.toLowerCase().trim()),
})
process.stdout.write(JSON.stringify({
  pluginViews: input.pluginArgs.map((args: string) => initial(parse(args))),
  workflowGroups: input.progress.map((events: unknown[]) => groups(events)),
  workflowFilters: input.filters.map((row: any) => filter(row.agents, row.filter)),
  finalValues: input.values.map((row: any) => final(Object.keys(row.schema), row.collected, row.schema, row.initial)),
}))
