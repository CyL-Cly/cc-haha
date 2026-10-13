import { describe, expect, test } from 'bun:test'
import { evaluatePairs, PAIRED_PROTOCOL, SCENARIOS, sha256, taskFor, type Pair } from './go-main-paired-benchmark-core'

const good = (_?: unknown, index = 0): Pair => ({ id: taskFor(SCENARIOS[index % SCENARIOS.length], Math.floor(index / SCENARIOS.length)).id, source: { success: true, durationMs: 100, estimatedCost: 1, runtimeErrors: [] }, go: { success: true, durationMs: 100, estimatedCost: 1, runtimeErrors: [] } })

describe('frozen paired benchmark judgments', () => {
  test('partial evidence cannot pass and all 120 observations are required', () => {
    expect(evaluatePairs(Array.from({ length: 119 }, good)).status).toBe('insufficient')
    const score = evaluatePairs(Array.from({ length: 120 }, good))
    expect(score.status).toBe('passed')
    if (score.status !== 'insufficient') expect(score.failureUpper95).toBeLessThan(PAIRED_PROTOCOL.failureMargin)
    const duplicate = Array.from({ length: 120 }, good)
    duplicate[1].id = duplicate[0].id
    expect(() => evaluatePairs(duplicate)).toThrow('Duplicate or unknown paired task')
  })
  test('one failure, unexpected error, changed cost or duration cannot pass', () => {
    for (const mutate of [
      (pair: Pair) => { pair.go.success = false },
      (pair: Pair) => { pair.source.success = false },
      (pair: Pair) => { pair.go.runtimeErrors.push('duplicate side effect') },
      (pair: Pair) => { pair.go.estimatedCost *= 1.051 },
      (pair: Pair) => { pair.go.durationMs *= 1.101 },
    ]) {
      const pairs = Array.from({ length: 120 }, good)
      pairs.forEach(mutate)
      expect(evaluatePairs(pairs).status).toBe('failed')
    }
  })
  test('high variance retains confidence uncertainty and no measurement can be missing', () => {
    const pairs = Array.from({ length: 120 }, good)
    pairs[0].go.estimatedCost = 1000
    expect(evaluatePairs(pairs).status).toBe('failed')
    pairs[0].go.estimatedCost = NaN
    const invalid = evaluatePairs(pairs)
    expect(invalid.status).toBe('failed')
    if (invalid.status !== 'insufficient') {
      expect(invalid.cost).toBeNull()
      expect(invalid.missingMeasurements).toEqual([{ id: pairs[0].id, engine: 'go', key: 'estimatedCost' }])
    }
    pairs[0].go.estimatedCost = 0
    pairs[0].go.success = false
    const timedOut = evaluatePairs(pairs)
    expect(timedOut.status).toBe('failed')
    if (timedOut.status !== 'insufficient') expect(timedOut.failedPairs).toBe(1)
  })
  test('tasks and hidden judgments are immutable, reproducible and variant-specific', () => {
    const ids = new Set<string>(), hashes = new Set<string>()
    for (const scenario of SCENARIOS) for (let repeat = 0; repeat < PAIRED_PROTOCOL.repeats; repeat++) {
      const task = taskFor(scenario, repeat)
      expect(task).toEqual(taskFor(scenario, repeat))
      expect(task.prompt.length).toBeGreaterThan(0)
      expect(task.judge.length).toBeGreaterThan(0)
      expect(task.fork).toBe(scenario === 'history-fork')
      for (const path of task.immutable) expect(task.files[path]).toBeDefined()
      ids.add(task.id)
      hashes.add(sha256(JSON.stringify(task)))
    }
    expect(ids.size).toBe(120)
    expect(hashes.size).toBe(120)
    expect(() => taskFor('skill', 20)).toThrow()
  })
})
