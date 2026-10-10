// Original registry inputs and raw model blocks precede both implementations.
// Lone names exercise Source's unknown-tool feedback; lone IDs remain distinct
// Source strings whose raw quoted identity must survive execution and feedback.
export type ToolIdentityScenario = { name: string; registry: { name: string; aliases: string[] }; usesSources: string[]; aborted?: boolean; deprecatedCatalog?: Array<{name:string;aliases:string[]}> }
const block = (name: string, id = '"call"') => `{"type":"tool_use","name":${name},"id":${id},"input":{"file_path":"identity.txt","content":"private"}}`
export const toolIdentityScenarios: ToolIdentityScenario[] = [
  { name: 'lone-high-name-primary-replacement', registry: { name: '�', aliases: [] }, usesSources: [block('"\\ud800"')] },
  { name: 'lone-low-name-alias-replacement', registry: { name: 'FixtureWrite', aliases:  ['�'] }, usesSources: [block('"\\udfff"')] },
  { name: 'literal-replacement-primary', registry: { name: '�', aliases: [] }, usesSources: [block('"\\ufffd"', '"\\ufffd"')] },
  { name: 'literal-replacement-alias', registry: { name: 'FixtureWrite', aliases:  ['�'] }, usesSources: [block('"�"')] },
  { name: 'escaped-emoji-name-and-id', registry: { name: '😀', aliases: [] }, usesSources: [block('"\\ud83d\\ude00"', '"\\ud83d\\ude00"')] },
  { name: 'escaped-ascii-id', registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [block('"FixtureWrite"', '"a\\u0062"')] },
  { name: 'lone-high-id', registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [block('"FixtureWrite"', '"\\ud800"')] },
  { name: 'lone-low-id', registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [block('"FixtureWrite"', '"\\udfff"')] },
  { name: 'later-lone-id-no-earlier-Go-effect', registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [block('"FixtureWrite"'), block('"FixtureWrite"', '"\\ud800"')] },
  { name: 'distinct-scalar-ids', registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [block('"FixtureWrite"', '"\\ufffd"'), block('"FixtureWrite"', '"😀"')] },
  {name:'unknown-name-does-not-end-batch',registry:{name:'FixtureWrite',aliases:['�']},usesSources:[block('"\\ud800"','"\\ud800"'),block('"�"','"\\udfff"')]},
  {name:'unknown-primitive-input',registry:{name:'FixtureWrite',aliases:[]},usesSources:['{"type":"tool_use","name":"\\ud800","id":"\\udfff","input":null}']},
  {name:'marker-before-unknown-and-cancel',registry:{name:'FixtureWrite',aliases:[]},aborted:true,usesSources:['{"type":"tool_use","name":"\\ud800","id":"\\udfff","input":{"__unparsedToolInput":{"raw":'+JSON.stringify('😀'.repeat(110)+'\ud800')+',"len":1e400,"future":true}}}']},
  {name:'unknown-before-known-cancel',registry:{name:'FixtureWrite',aliases:['�']},aborted:true,usesSources:[block('"\\ud800"','"\\ud800"'),block('"�"','"\\udfff"')]},
  {name:'extra-field-is-not-marker',registry:{name:'FixtureWrite',aliases:[]},usesSources:['{"type":"tool_use","name":"\\ud800","id":"call","input":{"__unparsedToolInput":{"raw":"x","len":1},"future":true}}']},
  {name:'marker-before-known-registry',registry:{name:'FixtureWrite',aliases:[]},usesSources:['{"type":"tool_use","name":"FixtureWrite","id":"\\ud800","input":{"__unparsedToolInput":{"raw":"lone\\udfff","len":5}}}']},

]
export type ToolIdentityInput = ToolIdentityScenario & { home: string }
export type ToolIdentityObservation = { lookupNames: Array<string | null>; calls: string[]; mapped: string[]; file: string | null; unsupported: boolean }

// Explicit release host snapshot, checked against actual getAllBaseTools() in
// each Source child. Descriptors do not authorize missing builtin handlers.
export const identityDeprecatedCatalog = [
  { name: 'Agent', aliases:  ['Task'] },
  { name: 'TaskOutput', aliases:  ['AgentOutputTool', 'BashOutputTool'] },
  { name: 'Bash', aliases: [] },
  { name: 'Glob', aliases: [] },
  { name: 'Grep', aliases: [] },
  { name: 'ExitPlanMode', aliases: [] },
  { name: 'Read', aliases: [] },
  { name: 'Edit', aliases: [] },
  { name: 'Write', aliases: [] },
  { name: 'NotebookEdit', aliases: [] },
  { name: 'WebFetch', aliases: [] },
  { name: 'TodoWrite', aliases: [] },
  { name: 'WebSearch', aliases: [] },
  { name: 'TaskStop', aliases:  ['KillShell'] },
  { name: 'AskUserQuestion', aliases: [] },
  { name: 'Skill', aliases: [] },
  { name: 'EnterPlanMode', aliases: [] },
  { name: 'EnterWorktree', aliases: [] },
  { name: 'ExitWorktree', aliases: [] },
  { name: 'SendMessage', aliases: [] },
  { name: 'ListSessions', aliases: [] },
  { name: 'ReadSession', aliases: [] },
  { name: 'CreateSession', aliases: [] },
  { name: 'SendSessionMessage', aliases: [] },
  { name: 'WaitSessions', aliases: [] },
  { name: 'Workflow', aliases: [] },
  { name: 'SendUserMessage', aliases:  ['Brief'] },
  { name: 'ImageGen', aliases: [] },
  { name: 'ImageEdit', aliases: [] },
  { name: 'ListMcpResourcesTool', aliases: [] },
  { name: 'ReadMcpResourceTool', aliases: [] },
  { name: 'ToolSearch', aliases: [] },
]

for (const [name, input] of [
  ['wrong-marker-raw','{"__unparsedToolInput":{"raw":false,"len":1}}'],
  ['wrong-marker-len','{"__unparsedToolInput":{"raw":"x","len":"1"}}'],
  ['null-marker','{"__unparsedToolInput":null}'],
  ['unknown-marker-key','{"future":{"raw":"x","len":1}}'],
  ['array-input','[]'],
  ['numeric-marker-zero','{"__unparsedToolInput":{"raw":"","len":0}}'],
] as const) toolIdentityScenarios.push({ name, registry: { name: 'FixtureWrite', aliases: [] }, usesSources: [`{"type":"tool_use","name":"\\ud800","id":"\\udfff","input":${input}}`]})
