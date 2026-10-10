import { parse } from 'acorn'
import { join } from 'node:path'
import { mkdirSync, writeFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from '../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Claude.ai all MCP merge Source')
if (process.env.CC_HAHA_MCP_ALL_FIXTURE !== '1') throw new Error('Missing all MCP marker')
await import('../../../preload')
process.env.CLAUDE_CONFIG_DIR = join(home, '.claude')
mkdirSync(process.env.CLAUDE_CONFIG_DIR, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR, 'settings.json'), JSON.stringify(input.settings))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home); state.setProjectRoot(home); state.setCwdState(home)
state.setAllowedSettingSources(['userSettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const api = await import('../../../src/services/mcp/config')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
resetSettingsCache()
const { getInitialSettings } = await import('../../../src/utils/settings/settings')
for (const key of ['allowedMcpServers', 'deniedMcpServers']) {
  if (JSON.stringify(getInitialSettings()[key]) !== JSON.stringify(input.settings[key])) throw new Error(`Source settings producer did not load ${key}`)
}
const text = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/services/mcp/config.ts', import.meta.url)).text())
const ast: any = parse(text, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n)
const source = (name: string) => {
  const n = declarations.find((n: any) => n?.type === 'FunctionDeclaration' && n.id.name === name)
  if (!n) throw new Error(`Missing actual Source function ${name}`)
  return text.slice(n.start, n.end)
}
const disabled = new Set(input.disabled)
const dedup = new Function('dependencies', `with(dependencies){${source('dedupClaudeAiMcpServers')};return dedupClaudeAiMcpServers}`)({
  getMcpServerSignature: api.getMcpServerSignature,
  isMcpServerDisabled: (name: string) => disabled.has(name),
  logForDebugging: () => {},
})
let fetches = 0
const original = new Function('dependencies', `with(dependencies){${source('getAllMcpConfigs')};return getAllMcpConfigs}`)({
  doesEnterpriseMcpConfigExist: () => false,
  // The independently Source-tested scoped configuration producer is a leaf
  // input here. The original all-scope orchestration/filter/dedup/merge executes.
  getClaudeCodeMcpConfigs: async () => ({ servers: input.manual, errors: [] }),
  fetchClaudeAIMcpConfigsIfEligible: async () => { fetches++; return input.organization },
  filterMcpServersByPolicy: api.filterMcpServersByPolicy,
  dedupClaudeAiMcpServers: dedup,
})
process.stdout.write(JSON.stringify({ result: await original(), fetches }) + '\n')
