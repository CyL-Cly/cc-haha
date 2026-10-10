import { assertDisposableFixtureHome } from './fixture-guard'
const home=assertDisposableFixtureHome(process.env.HOME,'Native MCP serve Source')
if(process.env.CC_HAHA_CLI_MCP_SERVE_SOURCE_FIXTURE!=='1')throw new Error('Unowned MCP serve fixture')
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setOriginalCwd(home);state.setCwdState(home);state.setProjectRoot(home)
const {enableConfigs}=await import('../../../src/utils/config')
enableConfigs()
// Run the real SDK transport, original catalogue/prompts/context and actual
// tool validators/calls. Bare avoids unrelated startup/account services and
// retains the original Bash/Read/Edit tool preset for this first lane.
const {startMCPServer}=await import('../../../src/entrypoints/mcp')
await startMCPServer(home,false,false)
