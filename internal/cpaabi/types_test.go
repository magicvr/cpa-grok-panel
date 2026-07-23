package cpaabi

import "testing"

func TestPluginRegistrationVersion(t *testing.T) {
	const want = "0.5.11"
	if PluginVersion != want {
		t.Fatalf("PluginVersion=%q want=%q", PluginVersion, want)
	}
	metadata, ok := PluginRegistration()["metadata"].(map[string]any)
	if !ok || metadata["Version"] != want {
		t.Fatalf("registration metadata=%v want Version=%q", metadata, want)
	}
}
