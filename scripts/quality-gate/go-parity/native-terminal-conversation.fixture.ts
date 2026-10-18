import fs from 'node:fs'
import { createRequire } from 'node:module'
import { marked } from 'marked'
import React from 'react'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual conversation command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONVERSATION_FIXTURE !== '1') throw new Error('Original conversation fixture guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })

function originals(relative: string, names: string[]) {
  const path = new URL('../../../' + relative, import.meta.url).pathname
  const source = ts.createSourceFile(path, fs.readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const selected = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && node.name && names.includes(node.name.text))
  if (selected.length !== names.length) throw new Error('Frozen original function identity changed: ' + relative)
  return new Bun.Transpiler({ loader: 'tsx' }).transformSync(selected.map((node: any) => node.getText(source)).join('\n')).replaceAll('export ', '')
}
const messageBody = originals('src/utils/messages.ts', ['extractTextContent', 'stripPromptXMLTags'])
const body = messageBody + '\n' + originals('src/commands/copy/copy.tsx', ['collectRecentAssistantTexts', 'fileExtension', 'extractCodeBlocks']) + '\n' + originals('src/commands/export/export.tsx', ['extractFirstPrompt', 'sanitizeFilename']) + '\n' + originals('src/utils/sessionTitle.ts', ['extractConversationText']) + '\nreturn {collectRecentAssistantTexts,fileExtension,extractCodeBlocks,extractFirstPrompt,sanitizeFilename,extractConversationText}'
const helpers = new Function('marked', 'MAX_LOOKBACK', 'MAX_CONVERSATION_TEXT', 'STRIPPED_TAGS_RE', body)(marked, 20, 1000, /<(commit_analysis|context|function_analysis|pr_analysis)>.*?<\/\1>\n?/gs)
const results = input.rows.map((row: any) => ({
  recent: helpers.collectRecentAssistantTexts(row.messages),
  blocks: helpers.extractCodeBlocks(row.markdown),
  extension: helpers.fileExtension(row.language),
  firstPrompt: helpers.extractFirstPrompt(row.messages),
  slug: helpers.sanitizeFilename(helpers.extractFirstPrompt(row.messages)),
  nameInput: helpers.extractConversationText(row.messages),
}))
const xml = ts.createSourceFile('xml.ts', fs.readFileSync(new URL('../../../src/constants/xml.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true)
const common: Record<string, string[]> = {}
for (const statement of xml.statements) {
  if (!ts.isVariableStatement(statement)) continue
  for (const declaration of statement.declarationList.declarations) {
    const name = declaration.name.getText(xml)
    if (name === 'COMMON_HELP_ARGS' || name === 'COMMON_INFO_ARGS') common[name] = new Function('return ' + declaration.initializer.getText(xml))()
  }
}
const tagSource = ts.createSourceFile('tag.tsx', fs.readFileSync(new URL('../../../src/commands/tag/tag.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const tagFunctions = tagSource.statements.filter((node: any) => ts.isFunctionDeclaration(node) && ['ConfirmRemoveTag', 'ToggleTagAndClose', 'ShowHelp', 'call'].includes(node.name?.text))
if (tagFunctions.length !== 4) throw new Error('Frozen original tag caller identity changed')
const tagBody = ts.transpileModule(tagFunctions.map((node: any) => node.getText(tagSource)).join('\n'), { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText.replaceAll('export ', '')
const tagCall = new Function('React', 'COMMON_HELP_ARGS', 'COMMON_INFO_ARGS', tagBody + '\nreturn call')(React, common.COMMON_HELP_ARGS, common.COMMON_INFO_ARGS)
const tagArgs = [...common.COMMON_HELP_ARGS, ...common.COMMON_INFO_ARGS, '', 'info', '--info', 'feature-auth', ' HELP ']
const tagCalls = []
for (const args of tagArgs) {
  const element = await tagCall(() => {}, {}, args)
  tagCalls.push({ args, help: element.type.name === 'ShowHelp', tagName: element.props.tagName })
}
const tag = (await import('../../../src/commands/tag/index')).default
const tagGate = []
for (const user of ['external', 'ant']) {
  process.env.USER_TYPE = user
  tagGate.push({ user, enabled: tag.isEnabled() })
}
process.env.USER_TYPE = 'external'
if (caps.blocked.length || caps.secureReads.length) throw new Error('Conversation helper escaped private source boundary')
process.stdout.write(JSON.stringify({ results, tagCalls, tagGate, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }))
