package all

import (
	"slices"
	"testing"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func TestBuiltinAdaptersRegistered(t *testing.T) {
	got := provider.DefaultRegistry.Adapters()
	for _, want := range []string{"anthropic", "chatgpt", "claudecode", "codex", "fake", "openai", "pi"} {
		if !slices.Contains(got, want) {
			t.Errorf("适配器 %q 未注册: %v", want, got)
		}
	}
}
