package toolschema

import "encoding/json"

func NativeImageDefinition(name string) (Definition, error) {
	if name != "ImageGen" && name != "ImageEdit" {
		return Definition{}, unsupported("image tool " + name)
	}
	one, min, max := 1, 1.0, 4.0
	prompt := textSchema("A complete visual prompt preserving all relevant user-specified detail", false)
	prompt.Native = true
	prompt.MinimumLength = &one
	count := Schema{Kind: "number", Native: true, Integer: true, Minimum: &min, Maximum: &max, Default: json.RawMessage("1"), Description: quote("Number of variations for this exact prompt, from 1 to 4")}
	enum := func(values []string, description string) Schema {
		return Schema{Kind: "enum", Values: values, Optional: true, Description: quote(description)}
	}
	fields := []Field{{"prompt", prompt}, {"count", count}, {"aspect_ratio", enum([]string{"auto", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2", "19.5:9", "9:19.5", "20:9", "9:20"}, "Requested output aspect ratio")}, {"resolution", enum([]string{"1k", "2k"}, "Requested image resolution when supported")}, {"size", enum([]string{"auto", "1024x1024", "1024x1536", "1536x1024"}, "OpenAI-compatible image size")}, {"quality", Schema{Kind: "enum", Values: []string{"auto", "low", "medium", "high"}, Optional: true}}, {"background", Schema{Kind: "enum", Values: []string{"auto", "opaque", "transparent"}, Optional: true}}, {"output_format", Schema{Kind: "enum", Values: []string{"png", "jpeg", "webp"}, Optional: true}}}
	description := "Generate brand-new images from a text prompt with the image provider configured for this desktop session. This tool does not accept source-image paths; use ImageEdit for edits."
	instructions := "Use this tool only for a brand-new image. It has no source-image argument; never invent or pass a placeholder path. Preserve the full relevant user specification. Provider and image model selection come from the current desktop session and are not tool arguments. One call represents one distinct prompt; use count only for variations of that same prompt. The tool saves finished raster images locally and returns their absolute paths. If a provider call fails, do not retry ImageGen automatically; explain the error and wait for the user to decide."
	hint := "generate images or artwork from a visual prompt"
	if name == "ImageEdit" {
		maxImages := 3
		path := textSchema("Exact absolute path to an image the user provided this session: an [Image source: ...] attachment, a file the user attached with @, or a prior ImageGen result", false)
		path.Native = true
		path.MinimumLength = &one
		fields = append(fields, Field{"referenced_image_paths", Schema{Kind: "array", Native: true, Items: &path, MinimumItems: &one, MaximumItems: &maxImages, Description: quote("Ordered source images to edit or use as visual references")}})
		description = "Edit images the user attached in this conversation — pasted, dropped, or attached with @ — or images returned by an earlier ImageGen call, using their exact source paths."
		instructions = "Use this tool only when the user wants to edit, combine, or visually reference existing images. referenced_image_paths is required and may contain only images the user supplied in this conversation: a path surfaced by [Image source: ...], a file the user attached with @, or a path returned by a prior ImageGen call. Never invent, search for, or substitute a path, and never open an image off disk yourself to feed it here — if the user means an image you have no path for, ask them to attach it. Preserve the full relevant user specification and repeat preservation constraints in every edit prompt. Provider and image model selection come from the current desktop session and are not tool arguments. One call represents one distinct prompt; use count only for variations of that same edit. If a provider call fails, do not retry ImageEdit automatically; explain the error and wait for the user to decide."
		hint = "edit or transform an attached or previously generated image"
	}
	return Definition{Kind: "builtin", Name: name, InputSchema: &Schema{Kind: "object", UnknownKeys: "strict", Fields: fields}, Description: quote(description), Prompt: quote(instructions), SearchHint: quote(hint), ShouldDefer: true, Strict: false}, nil
}
