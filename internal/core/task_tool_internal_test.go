package core

import "testing"

func TestResolveModel(t *testing.T) {
	models := []string{"claude-opus-5-5", "claude-sonnet-5-5"}
	for want, got := range map[string]string{"claude-opus-5-5": "claude-opus-5-5", "sonnet": "claude-sonnet-5-5", "Opus": "claude-opus-5-5", "haiku": "", "inherit": "", "": "", "gpt-x": ""} {
		if r := resolveModel(want, models); r != got {
			t.Errorf("resolveModel(%q) = %q, want %q", want, r, got)
		}
	}
}
