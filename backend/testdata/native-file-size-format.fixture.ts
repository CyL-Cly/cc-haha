import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'File size format Source')
if(process.env.CC_HAHA_FILE_SIZE_FORMAT_FIXTURE!=='1')throw new Error('Missing owned file size fixture')
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const {formatFileSize}=await import('../../src/utils/format')
process.stdout.write(JSON.stringify({sizes:input.sizes.map((size:number)=>formatFileSize(size)),fractions:input.fractions.map((n:number)=>formatFileSize(n*1024))}))
