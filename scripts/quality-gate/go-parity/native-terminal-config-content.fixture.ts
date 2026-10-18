import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original Config content pickers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONFIG_CONTENT_SOURCE !== '1') throw new Error('Missing Config content fixture guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = (file: string) => ts.createSourceFile(file, fs.readFileSync(new URL('../../../src/components/'+file, import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
const language = source('LanguagePicker.tsx')
let submit: any
const visit = (node: any) => {
  if (ts.isFunctionExpression(node) && node.name?.text === 'handleSubmit') submit = node
  ts.forEachChild(node, visit)
}
visit(language)
if (!submit) throw new Error('Missing original LanguagePicker submit callback')
const submitJS = ts.transpileModule('const run = '+submit.getText(language),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const languages = input.languages.map((value: any) => {
  let result: string | undefined
  new Function('language','onComplete',submitJS+';run()')(value, (next: string | undefined) => { result = next })
  return result ?? null
})
const styles = source('OutputStylePicker.tsx')
const mapper = styles.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'mapConfigsToOptions')
const constants = styles.statements.filter((node: any) => ts.isVariableStatement(node) && /DEFAULT_OUTPUT_STYLE_(LABEL|DESCRIPTION)/.test(node.getText(styles))).map((node: any) => node.getText(styles))
if (!mapper || constants.length !== 2) throw new Error('Missing original output style options mapper')
const mapperJS = ts.transpileModule([...constants,mapper.getText(styles)].join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const options = new Function(mapperJS+';return mapConfigsToOptions')( )(input.styles)
const config = source('Settings/Config.tsx')
const callbacks: Record<string, any> = {}
const scan = (node: any) => {
  if (ts.isJsxSelfClosingElement(node)) {
    const name = node.tagName.getText(config)
    if (name === 'OutputStylePicker' || name === 'LanguagePicker') {
      const attribute = node.attributes.properties.find((a: any) => a.name?.text === 'onComplete')
      callbacks[name] = attribute?.initializer?.expression
    }
  }
  ts.forEachChild(node,scan)
}
scan(config)
const writes: any[] = []
const runCallback = (name: string, value: any) => {
  if (!callbacks[name]) throw new Error('Missing Config '+name+' completion callback')
  const deps = { isDirty:{current:false},setCurrentOutputStyle:()=>{},setCurrentLanguage:()=>{},setShowSubmenu:()=>{},setTabsHidden:()=>{},DEFAULT_OUTPUT_STYLE_NAME:'default',updateSettingsForSource:(source: string,patch: any)=>writes.push({source,patch}),logEvent:()=>{} }
  const js = ts.transpileModule('const callback = '+callbacks[name].getText(config),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
  new Function(...Object.keys(deps),js+';callback(arguments[arguments.length-1])')(...Object.values(deps),value)
}
runCallback('OutputStylePicker','Fixture Style')
runCallback('LanguagePicker','日本語')
runCallback('LanguagePicker',undefined)
const sectionsSource = ts.createSourceFile('systemPromptSections.ts',fs.readFileSync(new URL('../../../src/constants/systemPromptSections.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const sections = sectionsSource.statements.filter((node: any)=>ts.isFunctionDeclaration(node) && ['systemPromptSection','resolveSystemPromptSections','clearSystemPromptSections'].includes(node.name?.text)).map((node: any)=>node.getText(sectionsSource).replace(/^export /,''))
if (sections.length !== 3) throw new Error('Original section-cache lifecycle absent')
const sectionsJS = ts.transpileModule(sections.join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
let cache = new Map()
const factory = new Function('getSystemPromptSectionCache','setSystemPromptSectionCacheEntry','clearSystemPromptSectionState','clearBetaHeaderLatches',sectionsJS+';return {systemPromptSection,resolveSystemPromptSections,clearSystemPromptSections}')(()=>cache,(name: string,value: string)=>cache.set(name,value),()=>{cache=new Map()},()=>{})
let languageValue = '日本語',styleValue = 'Fixture Style'
const cachedSections = () => [factory.systemPromptSection('language',()=>languageValue),factory.systemPromptSection('output_style',()=>styleValue)]
const cacheTrace = [await factory.resolveSystemPromptSections(cachedSections())]
languageValue='Deutsch';styleValue='Explanatory'
cacheTrace.push(await factory.resolveSystemPromptSections(cachedSections()))
factory.clearSystemPromptSections()
cacheTrace.push(await factory.resolveSystemPromptSections(cachedSections()))
process.stdout.write(JSON.stringify({languages,options,writes,cacheTrace}))
