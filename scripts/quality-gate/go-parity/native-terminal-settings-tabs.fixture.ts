import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Original Settings Tabs callbacks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_SETTINGS_TABS_SOURCE !== '1') throw new Error('Missing Settings Tabs fixture guard')
const ts = createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const settings = ts.createSourceFile('Settings.tsx',fs.readFileSync(new URL('../../../src/components/Settings/Settings.tsx',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
let initialExpression: any
const visitSettings = (node: any) => {
  if (ts.isJsxOpeningElement(node) && node.tagName.getText(settings) === 'Tabs') initialExpression = node.attributes.properties.find((a: any)=>a.name?.text==='initialHeaderFocused')?.initializer?.expression
  ts.forEachChild(node,visitSettings)
}
visitSettings(settings)
if (!initialExpression) throw new Error('Original Settings initial Tabs focus absent')
if (ts.isIdentifier(initialExpression)) {
  const name = initialExpression.text
  const findInitializer = (node: any) => {
    if (ts.isVariableDeclaration(node) && node.name.getText(settings)===name) initialExpression=node.initializer
    ts.forEachChild(node,findInitializer)
  }
  findInitializer(settings)
}
const initialJS = ts.transpileModule('const focus = '+initialExpression.getText(settings),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const initial = Object.fromEntries(['Status','Config','Usage'].map(tab => [tab.toLowerCase(),new Function('defaultTab',initialJS+';return focus')(tab)]))
const tabsSource = ts.createSourceFile('Tabs.tsx',fs.readFileSync(new URL('../../../src/components/design-system/Tabs.tsx',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
let change: any, down: any, active: any
const visit = (node: any) => {
  if (ts.isVariableDeclaration(node) && node.name.getText(tabsSource)==='handleTabChange') change=node.initializer
  if (ts.isVariableDeclaration(node) && node.name.getText(tabsSource)==='t6') active=node.initializer
  if (ts.isBinaryExpression(node) && node.left.getText(tabsSource)==='t8' && ts.isArrowFunction(node.right)) down=node.right
  ts.forEachChild(node,visit)
}
visit(tabsSource)
if (!change || !down || !active) throw new Error('Original Tabs callbacks absent')
const transpile = (name: string,node: any) => ts.transpileModule('const '+name+' = '+node.getText(tabsSource),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
let selected = input.kind, headerFocused = initial[selected], prevented = false
const headers = ['status','config','usage']
const trace = []
for (const step of input.steps) {
  if (step==='focusHeader') headerFocused = true
  else {
    const deps = { selectedTabIndex:headers.indexOf(selected),tabs:headers.map(t=>[t,t]),isControlled:true,onTabChange:(next: string)=>{selected=next},setInternalSelectedTab:()=>{},setHeaderFocused:(next: boolean)=>{headerFocused=next},headerFocused,optedIn:selected==='config',hidden:false,disableNavigation:false }
    if (step==='down') {
      new Function(...Object.keys(deps),transpile('handle',down)+';handle(arguments[arguments.length-1])')(...Object.values(deps),{key:'down',preventDefault(){prevented=true}})
    } else {
      const eligible = new Function(...Object.keys(deps),transpile('eligible',active)+';return eligible')(...Object.values(deps))
      if (eligible) new Function(...Object.keys(deps),transpile('handle',change)+';handle(arguments[arguments.length-1])')(...Object.values(deps),step==='previous'?-1:1)
    }
  }
  trace.push({kind:selected,headerFocused,prevented});prevented=false
}
process.stdout.write(JSON.stringify({initial,trace}))
