/** Expose a private source binding without redeclaring an upstream public export. */
export function exposeSourceBindings(source: string, names: string[], loader: 'ts' | 'tsx' = 'ts'): string {
  const exported = new Set(new Bun.Transpiler({ loader }).scan(source).exports)
  const missing = [...new Set(names)].filter(name => !exported.has(name))
  return missing.length === 0 ? source : `${source}\nexport { ${missing.join(', ')} }\n`
}
