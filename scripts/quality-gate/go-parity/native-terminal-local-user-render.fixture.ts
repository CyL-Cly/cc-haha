import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual local command UserTextMessage/SystemTextMessage mounted renderer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LOCAL_USER_RENDER_SOURCE !== '1') throw new Error('Pinned local user renderer Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-local-user-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
await Bun.write(join(home,'.claude','settings.json'),JSON.stringify({syntaxHighlightingDisabled:true}))
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
const { charInCellAt } = await import('../../../src/ink/screen')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { UserTextMessage } = await import('../../../src/components/messages/UserTextMessage')
const { Message } = await import('../../../src/components/Message')
const { Markdown } = await import('../../../src/components/Markdown')
const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
const { renderToAnsiString } = await import('../../../src/utils/staticRender')
const { Parser } = await import('../../../src/ink/termio')
const projection = (value: string) => {
  const cells = []
  for (const action of new Parser().feed(value)) if (action.type === 'text') for (const g of action.graphemes) cells.push({ text: g.value, style: action.style })
  return cells
}
const results = []
for (const row of input.rows) {
  if (row.markdown) {
    const node = React.createElement(ThemeProvider, { initialState: row.theme }, React.createElement(AppStateProvider, { initialState: getDefaultAppState() }, React.createElement(Markdown, null, row.text)))
    const original = await renderToAnsiString(node, row.width)
    results.push({ original, cells: projection(original), nativeCells: projection(row.nativeRows.join('\n')) })
    continue
  }
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = row.width
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const node = row.system ? React.createElement(Message,{message:{type:'system',subtype:'local_command',content:row.text,level:row.level??'info'},tools:[],commands:[],lookups:{},inProgressToolUseIDs:new Set(),progressMessagesForMessage:[],addMargin:false,verbose:false}) : React.createElement(UserTextMessage,{param:{type:'text',text:row.text},addMargin:false,verbose:false})
  const app = await render(React.createElement(AppStateProvider,{initialState:getDefaultAppState()},node), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  for (let i=0;i<10;i++) await new Promise(resolve=>setImmediate(resolve))
  await new Promise(resolve=>setTimeout(resolve,25))
  const screen=(instances.get(stdout) as any).frontFrame.screen
  const rows=[];for(let y=0;y<screen.height;y++){let line='';for(let x=0;x<screen.width;x++)line+=charInCellAt(screen,x,y)??' ';rows.push(line.trimEnd())}
  const text=rows.join('\n')
  let layout
  if(input.layout){const visit=(node:any):any=>({name:node.nodeName,text:node.nodeValue,layout:node.yogaNode?{width:node.yogaNode.getComputedWidth(),height:node.yogaNode.getComputedHeight(),left:node.yogaNode.getComputedLeft(),top:node.yogaNode.getComputedTop()}:undefined,children:node.childNodes?.map(visit)});layout=visit(instances.get(stdout)!.rootNode)}
  app.unmount()
  results.push({output,text,layout})
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Local UserMessage Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(results),error=>error?reject(error):resolve()))
process.exit(0)
