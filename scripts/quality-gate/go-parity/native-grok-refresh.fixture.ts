import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Grok file refresh lifecycle')
if (process.env.CC_HAHA_GROK_REFRESH_FIXTURE !== '1') throw new Error('Unowned Grok refresh fixture')
const { ensureFreshGrokTokens, forceRefreshGrokTokens, clearFreshGrokTokenCache } = await import('../../../src/services/grokAuth/refresh')
const now = Date.now
Date.now = () => 1770000000000
const rows = await Bun.stdin.json()
const output = []
try {
 for (const [i,row] of rows.entries()) {
  const path = join(process.env.HOME!, 'grok-' + i + '.json')
  process.env.GROK_OAUTH_FILE = path
  clearFreshGrokTokenCache()
  if (row.initial !== undefined) fs.writeFileSync(path, typeof row.initial === 'string' ? row.initial : JSON.stringify(row.initial), { mode:0o600 })
  const requests: any[] = []
  const fetchOverride = async (url: any, init: any) => {
   requests.push({ url:String(url), method:init.method, body:init.body, headers:[...new Headers(init.headers)] })
   const response = row.responses[Math.min(requests.length-1,row.responses.length-1)]
   return new Response(response.body,{status:response.status})
  }
  const calls=[]
  for (const force of row.force) {
   try { calls.push({ value:await (force ? forceRefreshGrokTokens({fetchOverride}) : ensureFreshGrokTokens({fetchOverride})) }) }
   catch(error) { calls.push({error:(error as Error).message}) }
  }
  output.push({calls,requests,file:fs.existsSync(path)?fs.readFileSync(path,'utf8'):null,mode:fs.existsSync(path)?fs.statSync(path).mode&0o777:null})
 }
} finally { Date.now=now }
process.stdout.write(JSON.stringify(output))
