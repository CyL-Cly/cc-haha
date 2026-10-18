import fs from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual plan local JSX command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLAN_COMMAND_SOURCE !== '1') throw new Error('Pinned plan Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-plan-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.setAllowedSettingSources(['userSettings']); bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const compiler = await import('react/compiler-runtime')
const { Box, Text } = await import('../../../src/ink')
const { renderToString } = await import('../../../src/utils/staticRender')
const { prepareContextForPlanMode } = await import('../../../src/utils/permissions/permissionSetup')
const { applyPermissionUpdate } = await import('../../../src/utils/permissions/PermissionUpdate')
const { toIDEDisplayName } = await import('../../../src/utils/ide')
const { getDefaultAppState } = await import('../../../src/state/AppState')
function original(path: string, deps: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node) && !ts.isExportAssignment(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + returned)(...Object.values(deps))
}
const planPath = input.planPath ?? join(home, 'source-plan.md')
const rows = []
for (const item of input.rows) {
  let state = getDefaultAppState()
  state.toolPermissionContext = { ...state.toolPermissionContext, mode: item.mode }
  const transitions: unknown[] = [], editors: unknown[] = [], done: unknown[] = []
  const call = original('../../../src/commands/plan/plan.tsx', {
    React, _c: compiler.c, Box, Text, renderToString, prepareContextForPlanMode, applyPermissionUpdate, toIDEDisplayName,
    handlePlanModeTransition: (before: string, after: string) => { transitions.push([before, after]); bootstrap.handlePlanModeTransition(before, after) },
    getPlan: () => item.plan, getPlanFilePath: () => planPath,
    getExternalEditor: () => item.editor || undefined,
    editFileInEditor: (path: string) => { editors.push(path); return { content: null, ...(item.editorError ? { error: item.editorError } : {}) } },
  }, 'call')
  await call((...values: any[]) => done.push(values), { getAppState: () => state, setAppState: (update: any) => state = update(state) }, item.args)
  rows.push({ done, mode: state.toolPermissionContext.mode, prePlanMode: state.toolPermissionContext.prePlanMode, transitions, editors })
}
const declaration = original('../../../src/commands/plan/index.ts', {}, 'plan')
if (caps.blocked.length || caps.secureReads.length) throw new Error('Plan command Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration, rows, planPath, capabilities: caps }), error => error ? reject(error) : resolve()))
process.exit(0)
