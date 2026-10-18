import { readFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { Cursor } from '../../../src/utils/Cursor'
import * as operators from '../../../src/vim/operators'
import { transition } from '../../../src/vim/transitions'
import { createInitialPersistentState, createInitialVimState } from '../../../src/vim/types'
import { lastGrapheme } from '../../../src/utils/intl'

assertDisposableFixtureHome(process.env.HOME, 'Actual Vim input state machine')
if (process.env.CC_HAHA_VIM_INPUT_FIXTURE !== '1') throw new Error('Unowned Vim input fixture')
const input = await Bun.stdin.json()
const source = readFileSync(new URL('../../../src/hooks/useVimInput.ts', import.meta.url), 'utf8')
const start = source.indexOf('export function useVimInput(')
if (start < 0) throw new Error('Actual Vim hook declaration missing')
// The actual hook body executes unchanged. Only React storage and controlled
// input I/O are supplied; all Vim transitions/operators/Cursor are Source.
const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start).replace(/^export /, ''))
const rows = []
for (const item of input.cases) {
  let text: string = item.text
  let offset: number = item.offset
  const references: any[] = []
  const undo: Array<{ text: string, offset: number }> = []
  const delegates: string[] = []
  const React = {
    useRef(value: any) { const ref = { current: value }; references.push(ref); return ref },
    useState(value: any) { return [value, (_value: any) => {}] },
  }
  const change = (value: string) => { undo.push({ text, offset }); text = value }
  const baseInput = (raw: string, key: any) => {
    delegates.push(key.name)
    let cursor = Cursor.fromText(text, item.columns, offset)
    let changed = false
    if (key.return) return
    if (key.ctrl) {
      switch (key.name) {
        case 'ctrl+a': cursor = cursor.startOfLogicalLine(); break
        case 'ctrl+e': cursor = cursor.endOfLogicalLine(); break
        case 'ctrl+b': cursor = cursor.left(); break
        case 'ctrl+f': cursor = cursor.right(); break
        case 'ctrl+h': cursor = cursor.backspace(); changed = true; break
        case 'ctrl+d': cursor = cursor.del(); changed = true; break
        default: return
      }
    } else if (key.leftArrow) cursor = cursor.left()
    else if (key.rightArrow) cursor = cursor.right()
    else if (key.upArrow) cursor = cursor.up()
    else if (key.downArrow) cursor = cursor.down()
    else if (key.backspace) { cursor = cursor.backspace(); changed = true }
    else if (key.delete) { cursor = cursor.del(); changed = true }
    else if (raw) { cursor = cursor.insert(raw); changed = true }
    if (changed) change(cursor.text)
    offset = cursor.offset
  }
  const props = {
    get value() { return text }, columns: item.columns,
    onChange: change,
    onUndo: () => { const value = undo.pop(); if (value) { text = value.text; offset = value.offset } },
  }
  const useTextInput = (_props: any) => ({ get offset() { return offset }, setOffset: (value: number) => { offset = value }, onInput: baseInput })
  const bindings: Record<string, any> = {
    React, useCallback: (value: any) => value, useState: React.useState,
    Cursor, lastGrapheme, transition, createInitialPersistentState, createInitialVimState,
    ...operators, useTextInput,
  }
  const names = Object.keys(bindings)
  const hook = new Function(...names, body + '\nreturn useVimInput')(...names.map(name => bindings[name]))
  const state = hook(props)
  if (item.mode === 'NORMAL') state.setMode('NORMAL')
  const observations = []
  for (const step of item.keys) {
    const name = step.name ?? ''
    const key = { name, ctrl: name.startsWith('ctrl+'), escape: name === 'esc', return: name === 'enter', leftArrow: name === 'left', rightArrow: name === 'right', upArrow: name === 'up', downArrow: name === 'down', backspace: name === 'backspace', delete: name === 'delete' }
    let error = null
    try { state.onInput(step.text ?? '', key) }
    catch (value) { error = { name: (value as Error).name, message: (value as Error).message } }
    observations.push({ text, offset, mode: references[0].current.mode, command: references[0].current.command ?? null, persistent: references[1].current, error })
    // Snapshot each original ref before subsequent commands mutate it.
    observations[observations.length - 1] = JSON.parse(JSON.stringify(observations[observations.length - 1]))
  }
  const debug = Cursor.fromText(text, item.columns, offset)
  rows.push({ name: item.name, observations, delegates, debug: { text: debug.text, offset: debug.offset, lines: debug.measuredText.getWrappedLines(), position: debug.getPosition() } })
}
await Bun.write(Bun.stdout, JSON.stringify(rows) + '\n')
