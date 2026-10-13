import { createHash } from 'node:crypto'

export const PAIRED_PROTOCOL = Object.freeze({
  version: 1,
  main: '6d21316ec4635f29ecbaa43e1a5574bfd31d7155',
  model: 'deepseek-flash',
  endpoint: 'https://api.deepseek.com/anthropic',
  thinking: 'disabled',
  contextWindow: 1_000_000,
  maxOutputTokens: 32_000,
  maxTurns: 20,
  repeats: 20,
  confidence: 0.95,
  failureMargin: 0.05,
  estimatedCostRatioLimit: 1.05,
  durationRatioLimit: 1.10,
  timeoutMs: 600_000,
})

export type Scenario = 'arithmetic' | 'csv' | 'unicode' | 'modules' | 'history-fork' | 'skill'
export const SCENARIOS: readonly Scenario[] = ['arithmetic', 'csv', 'unicode', 'modules', 'history-fork', 'skill']
export type Task = {
  id: string
  files: Record<string, string>
  prompt: string
  judge: string
  immutable: string[]
  fork: boolean
}

export function taskFor(scenario: Scenario, repetition: number): Task {
  if (!SCENARIOS.includes(scenario) || !Number.isInteger(repetition) || repetition < 0 || repetition >= PAIRED_PROTOCOL.repeats) throw new Error('Unknown frozen benchmark task')
  const nonce = `PAIR_${scenario.replaceAll('-', '_')}_${String(repetition).padStart(2, '0')}`
  const common = 'Work only in this disposable project. Complete the task with actual tools, run the tests, and report the result. Do not modify test_contract.py. '
  const a = repetition + 2, b = repetition + 7
  const make = (files: Record<string, string>, prompt: string, judge: string, fork = false): Task => ({ id: nonce, files, prompt, judge, immutable: Object.keys(files).filter(path => path === 'test_contract.py' || path === 'records.csv' || path === '输入-資料.txt' || path === 'seed.json'), fork })
  switch (scenario) {
    case 'arithmetic':
      return make({
        'calc.py': 'def add(a, b):\n    return a - b\n',
        'test_contract.py': `import unittest\nfrom calc import add\nclass Contract(unittest.TestCase):\n    def test_values(self):\n        self.assertEqual(add(${a}, ${b}), ${a + b})\n        self.assertEqual(add(-${a}, -${b}), -${a + b})\n        self.assertEqual(add(0, 0), 0)\n`,
      }, common + 'Repair add in calc.py so it adds two numbers. Run python3 -m unittest -v.', 'import calc\nassert calc.add(101, 209) == 310\nassert calc.add(-101, 209) == 108\n')
    case 'csv':
      return make({
        'records.csv': `name,amount\n"张三,团队",${a}\nAlice,${b}\n"quoted ""name""",3\n`,
        'aggregate.py': 'import csv\ndef total(path):\n    with open(path, encoding="utf-8", newline="") as f:\n        return sum(int(row["amount"]) for row in csv.DictReader(f)) - 1\n',
        'test_contract.py': `import unittest\nfrom aggregate import total\nclass Contract(unittest.TestCase):\n    def test_total(self):\n        self.assertEqual(total("records.csv"), ${a + b + 3})\n`,
      }, common + 'Repair aggregate.total, preserving proper quoted CSV parsing. Write report.json with nonce ' + nonce + ` and total ${a + b + 3}. Run python3 -m unittest -v.`, `import json,aggregate\nassert aggregate.total("records.csv") == ${a + b + 3}\nr=json.load(open("report.json"))\nassert r == {"nonce": "${nonce}", "total": ${a + b + 3}}\n`)
    case 'unicode':
      return make({
        '输入-資料.txt': `第一行\n${nonce}\n第三行 😀\n`,
        'textops.py': 'def middle(path):\n    with open(path, encoding="utf-8") as f:\n        return f.read().splitlines()[0]\n',
        'test_contract.py': `import unittest\nfrom textops import middle\nclass Contract(unittest.TestCase):\n    def test_middle(self):\n        self.assertEqual(middle("输入-資料.txt"), "${nonce}")\n`,
      }, common + 'Repair textops.middle to return the second line. Create 结果.csv as valid UTF-8 CSV with columns nonce,value and one row containing ' + nonce + ',第三行 😀. Run python3 -m unittest -v.', `import csv,textops\nassert textops.middle("输入-資料.txt") == "${nonce}"\nwith open("结果.csv", encoding="utf-8",newline="") as f:\n    assert list(csv.DictReader(f)) == [{"nonce":"${nonce}","value":"第三行 😀"}]\n`)
    case 'modules':
      return make({
        'numbers.py': 'def twice(x):\n    return x + 2\n',
        'app.py': 'from numbers import twice\ndef compute(x):\n    return twice(x) - 1\n',
        'test_contract.py': `import unittest\nfrom app import compute\nclass Contract(unittest.TestCase):\n    def test_compute(self):\n        self.assertEqual(compute(${a}), ${a * 2 + 1})\n        self.assertEqual(compute(-${a}), -${a * 2 - 1})\n`,
      }, common + 'Correct numbers.twice to multiply by 2 and app.compute to return twice(x)+1, including negative inputs. Run python3 -m unittest -v.', 'import app,numbers\nassert numbers.twice(100) == 200\nassert app.compute(-101) == -201\n')
    case 'history-fork':
      return make({ 'seed.json': JSON.stringify({ nonce }) + '\n' }, common + 'Continue the forked conversation. Write history.txt containing the exact complete fixed reply from the previous assistant turn, then read it back. Also create proof.json containing the nonce from seed.json. Do not infer the previous reply from a file or external service.', `import json\nassert open("history.txt").read().strip() == "SOURCE_LIVE_FULL_READY"\nassert json.load(open("proof.json")) == {"nonce":"${nonce}"}\n`, true)
    case 'skill':
      return make({
        'seed.json': JSON.stringify({ nonce, a, b }) + '\n',
        '.claude/skills/math-proof/SKILL.md': '---\nname: math-proof\ndescription: Produce the project math proof using actual files and commands.\n---\nRead seed.json. Create proof.json with exactly nonce and sum (a+b). Execute python3 -c to verify the JSON against seed.json. Report the command result.\n',
      }, '/math-proof', `import json\ns=json.load(open("seed.json"))\nassert json.load(open("proof.json")) == {"nonce":s["nonce"],"sum":s["a"]+s["b"]}\n`)
  }
}

export function sha256(bytes: string | Uint8Array): string { return createHash('sha256').update(bytes).digest('hex') }

export type Measurement = {
  success: boolean
  durationMs: number
  estimatedCost: number
  runtimeErrors: string[]
}
export type Pair = { id: string, source: Measurement, go: Measurement }

function ratioUpper(pairs: readonly Pair[], key: 'durationMs' | 'estimatedCost') {
  const values = pairs.map(pair => {
    if (!(pair.source[key] > 0) || !(pair.go[key] > 0) || !Number.isFinite(pair.source[key]) || !Number.isFinite(pair.go[key])) throw new Error('Missing positive paired measurement')
    return Math.log(pair.go[key]) - Math.log(pair.source[key])
  })
  const mean = values.reduce((sum, value) => sum + value, 0) / values.length
  const variance = values.reduce((sum, value) => sum + (value - mean) ** 2, 0) / (values.length - 1)
  // Frozen one-sided t(119), 95% quantile. Only the complete 120-pair batch is
  // eligible; there is no optional stopping or retrospective margin change.
  const t = 1.657759284926144
  return { geometricMean: Math.exp(mean), upper95: Math.exp(mean + t * Math.sqrt(variance / values.length)) }
}

export function evaluatePairs(pairs: readonly Pair[]) {
  const required = SCENARIOS.length * PAIRED_PROTOCOL.repeats
  if (pairs.length !== required) return { status: 'insufficient' as const, pairs: pairs.length, required }
  const expected = new Set(SCENARIOS.flatMap(scenario => Array.from({ length: PAIRED_PROTOCOL.repeats }, (_, repeat) => taskFor(scenario, repeat).id)))
  for (const pair of pairs) if (!expected.delete(pair.id)) throw new Error('Duplicate or unknown paired task')
  const failures = pairs.filter(pair => !pair.go.success || !pair.source.success)
  const goOnlyFailures = pairs.filter(pair => !pair.go.success && pair.source.success).length
  const unexpected = pairs.filter(pair => pair.go.runtimeErrors.length > 0 || pair.source.runtimeErrors.length > 0)
  const failureUpper95 = goOnlyFailures === 0 ? 1 - 0.05 ** (1 / pairs.length) : null
  const missingMeasurements = pairs.flatMap(pair => (['source', 'go'] as const).flatMap(engine => (['estimatedCost', 'durationMs'] as const).filter(key => !(pair[engine][key] > 0) || !Number.isFinite(pair[engine][key])).map(key => ({ id: pair.id, engine, key }))))
  const cost = missingMeasurements.some(row => row.key === 'estimatedCost') ? null : ratioUpper(pairs, 'estimatedCost')
  const duration = missingMeasurements.some(row => row.key === 'durationMs') ? null : ratioUpper(pairs, 'durationMs')
  const accepted = failures.length === 0 && unexpected.length === 0 && missingMeasurements.length === 0 && failureUpper95 !== null && failureUpper95 <= PAIRED_PROTOCOL.failureMargin && cost !== null && cost.upper95 <= PAIRED_PROTOCOL.estimatedCostRatioLimit && duration !== null && duration.upper95 <= PAIRED_PROTOCOL.durationRatioLimit
  return { status: accepted ? 'passed' as const : 'failed' as const, pairs: pairs.length, failedPairs: failures.length, unexpectedPairs: unexpected.length, goOnlyFailures, failureUpper95, missingMeasurements, cost, duration, billing: 'Original SDK estimate, not a DeepSeek invoice', inferenceScope: 'These frozen tasks, parameters, versions and platform; no claim of identical stochastic scores' }
}
