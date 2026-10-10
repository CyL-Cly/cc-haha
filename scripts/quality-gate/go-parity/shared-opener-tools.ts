import { spyOn } from 'bun:test'
import type { Tool } from '../../../src/Tool'
import type { ToolDefinition } from './tool-schema-fixtures'

// Test-only original definitions instantiate actual Source tool/schema bodies.
// Never accept a Source-rendered prompt or final Anthropic native schema.
export async function instantiateSharedOpenerTools(definitions: ToolDefinition[], promptCalls: Record<string, number>): Promise<Tool[]> {
  const { z } = await import('zod/v4')
  const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
  const { FileEditTool } = await import('../../../src/tools/FileEditTool/FileEditTool')
  const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
  const { GlobTool } = await import('../../../src/tools/GlobTool/GlobTool')
  const { GrepTool } = await import('../../../src/tools/GrepTool/GrepTool')
  const native: Record<string, Tool> = { Read: FileReadTool, Edit: FileEditTool, Write: FileWriteTool, Glob: GlobTool, Grep: GrepTool }
  const wrapped = new WeakSet<object>()
  return definitions.map(definition => {
    let tool = native[definition.name]
    if (definition.kind === 'registered') {
      if (definition.inputSchema?.kind !== 'object' || definition.inputSchema.unknownKeys !== 'strip' || definition.inputSchema.fields?.some(field => field.schema.kind !== 'string')) throw new Error('Shared opener requires original registered string/object schema')
      tool = { name: definition.name, aliases: definition.aliases, inputSchema: z.object(Object.fromEntries((definition.inputSchema.fields ?? []).map(field => [field.name, z.string()]))), prompt: async () => definition.prompt, description: async () => definition.description, isConcurrencySafe: () => definition.concurrencySafe ?? false, isReadOnly: () => definition.readOnly ?? false, isEnabled: () => true } as Tool
    } else if (definition.kind !== 'builtin' || !tool) throw new Error('Shared opener requires a declared actual Source native tool')
    if (!wrapped.has(tool)) {
      wrapped.add(tool)
      const original = tool.prompt
      spyOn(tool, 'prompt').mockImplementation(async options => {
        promptCalls[tool.name] = (promptCalls[tool.name] ?? 0) + 1
        return original.call(tool, options)
      })
    }
    return tool
  })
}
