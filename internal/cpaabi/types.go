package cpaabi

const (
	PluginID      = "cpa-grok-panel"
	PluginVersion = "0.2.2"
	ABIVersion    = 1
)

// PluginRegistration returns CPA-compatible registration payload.
func PluginRegistration() map[string]any {
	return map[string]any{
		"schema_version": ABIVersion,
		"metadata": map[string]any{
			"Name":             PluginID,
			"Version":          PluginVersion,
			"Author":           "magicvr",
			"GitHubRepository": "https://github.com/magicvr/cpa-grok-panel",
			"Logo":             "",
			"ConfigFields":     []any{},
		},
		"capabilities": map[string]any{
			"management_api": true,
			"usage_plugin":   true,
		},
	}
}

// ManagementResponse matches pluginapi.ManagementResponse JSON shape.
// Headers must be map[string][]string (http.Header), not map[string]string.
type ManagementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}
