import fs from 'node:fs'
import { parse } from 'acorn'
import OptionMap from '../../../../src/components/CustomSelect/option-map'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_PICKER_NAVIGATION_FIXTURE !== '1'
  || Object.keys(input).some(key => !['home', 'cases'].includes(key))) throw new Error('Original select navigation requires private inputs and Bun 1.3.14')
const home = assertDisposableFixtureHome(input.home, 'model picker navigation')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
// The hook's original pure reducer is not exported. Extract its exact AST
// initializers from the checked-out Source, without reimplementing the oracle.
const path = new URL('../../../../src/components/CustomSelect/use-select-navigation.ts', import.meta.url)
const text = fs.readFileSync(path, 'utf8')
const compiled = new Bun.Transpiler({ loader: 'ts' }).transformSync(text)
const source = parse(compiled, { ecmaVersion: 'latest', sourceType: 'module' })
const names = new Set(['reducer', 'createDefaultState'])
const originals: string[] = []
for (const node of source.body) {
  if (node.type !== 'VariableDeclaration') continue
  for (const declaration of node.declarations) {
    if (declaration.id.type === 'Identifier' && names.has(declaration.id.name) && declaration.init) {
      originals.push(`const ${declaration.id.name} = ${compiled.slice(declaration.init.start, declaration.init.end)}`)
      names.delete(declaration.id.name)
    }
  }
}
if (names.size) throw new Error('Original navigation entrypoints missing')
const { reducer, createDefaultState } = new Function('OptionMap', `${originals.join('\n')}\nreturn { reducer, createDefaultState }`)(OptionMap)
const sentinel = '__NO_PREFERENCE__'
const rows = input.cases.map((record: any) => {
  const options = record.values.map((value: string | null) => ({ value: value ?? sentinel, label: value ?? 'Default' }))
  const initial = record.initial ?? sentinel
  const initialFocusValue = options.some((option: any) => option.value === initial) ? initial : options[0]?.value
  let state = createDefaultState({ options, visibleOptionCount: Math.min(10, options.length), initialFocusValue })
  const snapshot = () => ({
    focused: state.focusedValue === undefined ? -1 : state.optionMap.get(state.focusedValue).index,
    from: state.visibleFromIndex,
    to: state.visibleToIndex,
    highlighted: options.flatMap((option: any, index: number) => option.value === state.focusedValue ? [index] : []),
    visible: options.map((_: any, index: number) => index).slice(state.visibleFromIndex, state.visibleToIndex),
  })
  const steps = [snapshot()]
  for (const type of record.actions) {
    state = reducer(state, { type })
    steps.push(snapshot())
  }
  return steps
})
process.stdout.write(JSON.stringify({ rows, capabilities }))
