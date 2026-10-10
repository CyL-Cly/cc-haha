import { assertDisposableFixtureHome } from './fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir,'CLI MCP primitives')
if(process.env.CC_HAHA_CLI_MCP_PRIMITIVES_FIXTURE!=='1')throw new Error('Unowned MCP primitives')
await import('../../../preload')
const {parseHeaders,ensureTransport}=await import('../../../src/services/mcp/utils')
const {parseEnvVars}=await import('../../../src/utils/envUtils')
process.stdout.write(JSON.stringify(input.rows.map((row:any)=>{try{return {value:row.kind==='headers'?parseHeaders(row.values):row.kind==='env'?parseEnvVars(row.values):ensureTransport(row.value)}}catch(error){return {error:(error as Error).message}}})))
