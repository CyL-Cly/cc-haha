import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import chalk from 'chalk'
import figures from 'figures'
import stripAnsi from 'strip-ansi'
import { plot as asciichart } from 'asciichart'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual terminal context/stats')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONTEXT_STATS_FIXTURE !== '1') throw new Error('Frozen Source primitive required')
// Prepare the repository's compiler dependency before installing the Source
// runtime capability boundary (TypeScript probes filesystem case sensitivity).
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
chalk.level = 3
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
state.setIsInteractive(true)
process.chdir(home)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const settingsCache = await import('../../../src/utils/settings/settingsCache')
const effort = await import('../../../src/commands/effort/effort')
const effortIndex = (await import('../../../src/commands/effort/index')).default
const statsIndex = (await import('../../../src/commands/stats/index')).default
const contextIndex = (await import('../../../src/commands/context/index')).context
const { ansiToPng } = await import('../../../src/utils/ansiToPng')
const { generateHeatmap } = await import('../../../src/utils/heatmap')
const { renderModelName } = await import('../../../src/utils/model/model')
const { formatDuration, formatNumber } = await import('../../../src/utils/format')
const { getTheme, themeColorToAnsi } = await import('../../../src/utils/theme')
const { resolveThemeSetting } = await import('../../../src/utils/systemTheme')
const { applyColor } = await import('../../../src/ink/colorize')
const { stringWidth } = await import('../../../src/ink/stringWidth')
const rows = []
for (const row of input.effort ?? []) {
  delete process.env.CLAUDE_CODE_EFFORT_LEVEL
  if (row.env !== undefined) process.env.CLAUDE_CODE_EFFORT_LEVEL = row.env
  fs.mkdirSync(path.join(home, '.claude'), { recursive: true })
  fs.writeFileSync(path.join(home, '.claude', 'settings.json'), JSON.stringify(row.settings))
  settingsCache.resetSettingsCache()
  const result = effort.executeEffort(row.args, 'fixture-model')
  rows.push({ result, settings: JSON.parse(fs.readFileSync(path.join(home, '.claude', 'settings.json'), 'utf8')) })
}
const statsFile = new URL('../../../src/components/Stats.tsx', import.meta.url).pathname
const statsSource = ts.createSourceFile(statsFile, fs.readFileSync(statsFile, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const names = ['getNextDateRange', '_temp7', '_temp9']
const selected = statsSource.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
if (selected.length !== names.length) throw new Error('Original stats function identities changed')
const body = new Bun.Transpiler({ loader: 'tsx' }).transformSync(selected.map((node: any) => node.getText(statsSource)).join('\n'))
const helpers = new Function('DATE_RANGE_ORDER', body + '\nreturn {getNextDateRange,_temp7,_temp9}')(['all', '7d', '30d'])
const renderNames = ['renderStatsToAnsi', 'renderOverviewToAnsi', 'renderModelsToAnsi', 'generateTokenChart', 'generateXAxisLabels', 'generateFunFactoid']
const renderFunctions = statsSource.statements.filter((node: any) => ts.isFunctionDeclaration(node) && renderNames.includes(node.name?.text))
const constants = statsSource.statements.filter((node: any) => ts.isVariableStatement(node) && node.declarationList.declarations.some((declaration: any) => ['BOOK_COMPARISONS', 'TIME_COMPARISONS'].includes(declaration.name.getText(statsSource))))
if (renderFunctions.length !== renderNames.length || constants.length !== 2) throw new Error('Original screenshot renderer identities changed')
const renderBody = new Bun.Transpiler({ loader: 'tsx' }).transformSync([...constants, ...renderFunctions].map((node: any) => node.getText(statsSource)).join('\n'))
const render = new Function('chalk', 'figures', 'stripAnsi', 'asciichart', 'generateHeatmap', 'renderModelName', 'formatDuration', 'formatNumber', 'getTheme', 'themeColorToAnsi', 'resolveThemeSetting', 'getGlobalConfig', 'applyColor', 'getStringWidth', 'feature', renderBody + '\nreturn renderStatsToAnsi')(chalk, figures, stripAnsi, asciichart, generateHeatmap, renderModelName, formatDuration, formatNumber, getTheme, themeColorToAnsi, resolveThemeSetting, config.getGlobalConfig, applyColor, stringWidth, () => false)
chalk.level = 3
const OriginalDate = globalThis.Date
if (input.now) {
  const fixed = new OriginalDate(input.now).getTime()
  globalThis.Date = class extends OriginalDate {
    constructor(...args: any[]) { super(...(args.length ? args : [fixed]) as [any]) }
    static now() { return fixed }
  } as DateConstructor
}
const screenshots = (input.render ?? []).map((row: any) => render(row.stats, row.tab))
globalThis.Date = OriginalDate
const png = [...(input.images ?? []), ...screenshots].map((text: string) => ansiToPng(text).toString('base64'))
const stats = (input.stats ?? []).map((row: any) => {
  const models = Object.entries(row.modelUsage).sort(helpers._temp7)
  return { models: models.map(([name]) => name), totalTokens: models.reduce(helpers._temp9, 0) }
})
state.setIsInteractive(true)
const contextInteractive = contextIndex.isEnabled?.()
state.setIsInteractive(false)
const contextNonInteractive = contextIndex.isEnabled?.()
const registrations = [contextIndex, effortIndex, statsIndex].map(command => ({ name: command.name, kind: command.type, description: command.description, argumentHint: command.argumentHint }))
if (caps.blocked.length || caps.secureReads.length) throw new Error('Actual context/stats Source escaped private boundary: ' + JSON.stringify({ blocked: caps.blocked, secureReads: caps.secureReads }))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, png, stats, screenshots, ranges: ['all', '7d', '30d'].map(helpers.getNextDateRange), registrations, contextInteractive, contextNonInteractive, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), () => resolve()))
process.exit(0)
