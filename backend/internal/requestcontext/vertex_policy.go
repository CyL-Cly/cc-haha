package requestcontext

import "github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"

func IsNativeVertexEnvironment(name string) bool {
	switch name {
	case "CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID", "CLOUD_ML_REGION", "CLAUDE_CODE_SKIP_VERTEX_AUTH", "DISABLE_INTERLEAVED_THINKING", "CLAUDE_CODE_DISABLE_1M_CONTEXT":
		return true
	}
	return false
}

func nativeVertexRequestBetas(input Context) ([]string, error) {
	canonical, err := input.Host.NativeVertexCanonical(input.Options.Model)
	if err != nil {
		return nil, err
	}
	return cloudauth.VertexBetas(input.Options.Model, canonical, input.Host.Env, input.Host.SDKBetas), nil
}
