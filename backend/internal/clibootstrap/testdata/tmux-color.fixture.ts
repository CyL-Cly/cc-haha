import fs from 'node:fs'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text())
assertDisposableFixtureHome(input.home,'Tmux color primitive')
const text=fs.readFileSync(new URL('../../../../node_modules/chalk/source/vendor/supports-color/index.js',import.meta.url),'utf8')
const code=text.split('\n').filter(line=>!line.startsWith('import ')).join('\n').replace('export function createSupportsColor','function createSupportsColor').replace('export default supportsColor;','return supportsColor;')
const rows=input.rows.map(row=>{const value=new Function('process','tty','os',code)({platform:'darwin',argv:['go-cli',...row.args],env:row.env},{isatty:()=>row.tty},{release:()=>''});return !!value.stdout.hasBasic})
process.stdout.write(JSON.stringify(rows))
