import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual Plan auto context')
if (process.env.CC_HAHA_AUTO_PLAN_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Bun.version !== '1.3.14') throw new Error('Explicit private pinned recipe required')
const caps = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const b = await import('../../../src/bootstrap/state')
const cwd=home+"/work"; b.setCwdState(cwd); b.setOriginalCwd(cwd); b.setProjectRoot(cwd); b.setIsInteractive(false); b.setInitialMainLoopModel('fixture-model'); b.switchSession('11111111-1111-4111-8111-111111111111')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const settings = await import('../../../src/utils/settings/settings')
if (input.flag) b.setFlagSettingsInline(input.flag)
const setup = await import('../../../src/utils/permissions/permissionSetup')
const auto = await import('../../../src/utils/permissions/autoModeState')
const { EnterPlanModeTool } = await import('../../../src/tools/EnterPlanModeTool/EnterPlanModeTool')
const { ExitPlanModeV2Tool } = await import('../../../src/tools/ExitPlanModeTool/ExitPlanModeV2Tool')
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
let app: any = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: input.mode || 'default' } }
const context: any = { getAppState: () => app, setAppState: (fn: any) => { app = fn(app) }, options: { tools: [EnterPlanModeTool, ExitPlanModeV2Tool, BashTool], mainLoopModel: 'fixture-model' }, messages: [], abortController: new AbortController() }
const observe = () => ({ permission: { ...app.toolPermissionContext, additionalWorkingDirectories: [...app.toolPermissionContext.additionalWorkingDirectories] }, active: auto.isAutoModeActive(), needsAutoExit: b.needsAutoModeExitAttachment(), needsPlanExit: b.needsPlanModeExitAttachment() })
const before = { optIn: settings.hasAutoModeOptIn(), useDuringPlan: settings.getUseAutoModeDuringPlan(), planUse: setup.shouldPlanUseAutoMode(), ...observe() }
const entered = await EnterPlanModeTool.call({}, context)
const afterEnter = observe()
let classified: any
if (input.classify) {
 const { classifyYoloAction, formatActionForClassifier } = await import('../../../src/utils/permissions/yoloClassifier')
 const history = [{ type: 'user', message: { role: 'user', content: 'Use the private fixture workspace only' } }]
 if(input.permission){
 const { hasPermissionsToUseTool }=await import('../../../src/utils/permissions/permissions')
 context.messages=history
 classified=await hasPermissionsToUseTool(BashTool,{command:'printf safe'},context,{type:'assistant',uuid:'original-assistant-id',message:{id:'msg-original',role:'assistant',content:[]}} as any,'original-tool-id')
} else classified = await classifyYoloAction(history as any, formatActionForClassifier('Bash', { command: 'printf safe' }), context.options.tools, app.toolPermissionContext, context.abortController.signal)
}
let exited: any
if(input.exit === 'transition') {
 const { applyPermissionUpdate }=await import('../../../src/utils/permissions/PermissionUpdate')
 const next=setup.transitionPermissionMode(app.toolPermissionContext.mode, 'default', app.toolPermissionContext)
 app={...app,toolPermissionContext:applyPermissionUpdate(next,{type:'setMode',mode:'default',destination:'session'})}
 exited={declaredExportedTransition:true}
} else {try {exited=await ExitPlanModeV2Tool.call({plan:'Private actual plan'},context)} catch(error){exited={error:{name:(error as Error).name,message:(error as Error).message,stack:(error as Error).stack}}}}
await Bun.write(1, JSON.stringify({ before, entered, afterEnter, ...(classified === undefined ? {} : { classified }), exited, afterExit: observe(), caps }) + '\n')
