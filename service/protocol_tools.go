package service

import "strings"

// ClassifyProtocolTool describes who executes protocol-defined tools. Versioned
// Claude families retain their execution owner across dated schemas. References:
// https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-reference
// https://developers.openai.com/api/docs/guides/tools-shell
// https://developers.openai.com/api/docs/guides/tools-tool-search
func ClassifyProtocolTool(spec map[string]any) string {
	kind, _ := spec["type"].(string)
	switch kind {
	case "", "function", "custom", "namespace", "local_shell", "apply_patch", "computer", "computer_use_preview":
		return "client"
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "file_search", "code_interpreter", "image_generation", "mcp", "mcp_toolset":
		return "server"
	case "tool_search":
		if spec["execution"] == "client" {
			return "client"
		}
		if spec["execution"] == nil || spec["execution"] == "server" {
			return "server"
		}
		return "unknown"
	case "shell":
		environment, _ := spec["environment"].(map[string]any)
		if environment["type"] == "local" {
			return "client"
		}
		if environment["type"] == "container_auto" || environment["type"] == "container_reference" {
			return "server"
		}
		return "unknown"
	}
	for _, family := range []string{"bash_", "text_editor_", "computer_", "computer_toolset_", "browser_toolset_", "memory_"} {
		if datedProtocolTool(kind, family) {
			return "client"
		}
	}
	for _, family := range []string{"web_search_", "web_fetch_", "code_execution_", "advisor_", "tool_search_tool_regex_", "tool_search_tool_bm25_"} {
		if datedProtocolTool(kind, family) {
			return "server"
		}
	}
	return "unknown"
}
func datedProtocolTool(kind, family string) bool {
	if !strings.HasPrefix(kind, family) {
		return false
	}
	suffix := strings.TrimPrefix(kind, family)
	if len(suffix) != 8 {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
