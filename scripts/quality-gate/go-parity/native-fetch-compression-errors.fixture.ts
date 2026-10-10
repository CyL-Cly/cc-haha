import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native fetch compression errors')
if (process.env.CC_HAHA_FETCH_COMPRESSION_ERROR_FIXTURE !== '1') throw new Error('Unowned compression error fixture')
const rows = JSON.parse(await Bun.stdin.text())
const output = []
for (const [index, row] of rows.entries()) {
 const url = new URL(row.url)
 if (!['127.0.0.1','localhost'].includes(url.hostname)) throw new Error('Non-loopback compression error fixture')
 const response = await fetch(url,{headers:{'X-Fixture-Run':'source'}})
 process.stdout.write(JSON.stringify({ready:index})+'\n')
 try { await response.arrayBuffer(); output.push({status:response.status,error:null}) }
 catch(error) { output.push({status:response.status,error:{message:(error as Error).message,code:(error as any).code}}) }
}
process.stdout.write(JSON.stringify(output)+'\n')
