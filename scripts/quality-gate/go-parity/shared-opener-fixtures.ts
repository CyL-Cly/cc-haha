import { requestRetryProfile, type RequestRetryMode } from './request-retry-fixtures'
import { fixtureWriteDefinition, type ToolDefinition } from './tool-schema-fixtures'

export type SharedOpenerCase = typeof requestRetryProfile & {
  name: string
  mode: RequestRetryMode
  nonInteractive: boolean
  querySource: string
  toolDefinitions?: ToolDefinition[]
  extraToolSchemaSources?: string[]
  toolChoiceSource?: string
}
const native = ['Read', 'Edit', 'Write', 'Glob', 'Grep'].map(name => ({ kind: 'builtin', name }) as ToolDefinition)
const assistant = '{"type":"assistant","message":{"id":"original-message","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"tool_use","id":"call\\ud800","name":"FixtureWrite","input":{"content":"text","legacy_view":"kept","file_path":"private.txt","\\udfff":"future"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":2}},"uuid":"original-assistant","timestamp":"2023-11-14T22:13:20.000Z"}'
const result = (content: string | undefined) => '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call\\ud800"' + (content === undefined ? '' : ',"content":' + content) + '}]},"uuid":"original-result","timestamp":"2023-11-14T22:13:20.000Z"}'
const paired = (content: string | undefined) => [...requestRetryProfile.historySources, assistant, result(content)]
const profile = (name: string, rest: Partial<SharedOpenerCase> = {}): SharedOpenerCase => ({ ...requestRetryProfile, name, mode: 'sequence', nonInteractive: false, querySource: 'repl_main_thread', ...rest })
export const sharedOpenerCases: SharedOpenerCase[] = [
  profile('bare interactive original env key'),
  profile('bare interactive actual native Read', { toolDefinitions: [native[0]] }),
  profile('headless actual five native tools', { nonInteractive: true, querySource: 'sdk', toolDefinitions: native }),
  profile('registered native paired textual tool result', { toolDefinitions: [fixtureWriteDefinition], historySources: paired('"written"') }),
  profile('paired nested text strips tool reference preserves UTF16', { toolDefinitions: [fixtureWriteDefinition], historySources: paired('[{"type":"tool_reference","tool_name":"FixtureWrite"},{"type":"text","text":"visible\\udfff"}]') }),
  profile('paired null tool result', { toolDefinitions: [fixtureWriteDefinition], historySources: paired('null') }),
  profile('paired missing tool result content', { toolDefinitions: [fixtureWriteDefinition], historySources: paired(undefined) }),
  profile('ordered native and original extra tools', { toolDefinitions: native, extraToolSchemaSources: ['{"name":"extra","type":"custom-extra"}'] }),
  profile('original server tool schema and auto choice', { extraToolSchemaSources: ['{"type":"web_search_20250305","name":"web_search"}'], toolChoiceSource: '{"type":"auto"}' }),
  profile('original any tool choice keeps parallel flag', { toolDefinitions: [fixtureWriteDefinition], toolChoiceSource: '{"type":"any","disable_parallel_tool_use":true}' }),
  profile('original explicit native tool choice', { toolDefinitions: [fixtureWriteDefinition], toolChoiceSource: '{"type":"tool","name":"FixtureWrite"}' }),
  profile('abort before still prepares actual native tools once', { mode: 'abort-before', toolDefinitions: [fixtureWriteDefinition], historySources: paired('"written"') }),
  profile('stop at notice does not reprepare or send', { mode: 'stop-notice', toolDefinitions: [fixtureWriteDefinition] }),
  profile('overflow retains tools and original paired history', { mode: 'overflow', toolDefinitions: [fixtureWriteDefinition], historySources: paired('"written"') }),
]

// Keep the historical fourteen captured goldens unchanged. These profiles
// prove the later real media admission against current Source and raw HTTP.
export const sharedOpenerMediaCases: SharedOpenerCase[] = [
  profile('original orphan image tool result resumes normally', {
    historySources: ['{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}}'],
  }),
  profile('paired processed image tool result reaches physical request', {
    toolDefinitions: [fixtureWriteDefinition],
    historySources: paired('[{"type":"text","text":"image from tool"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]'),
  }),
  profile('paired document tool result reaches physical request', {
    toolDefinitions: [fixtureWriteDefinition],
    historySources: paired('[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERg=="},"title":"Original tool PDF"}]'),
  }),
]
