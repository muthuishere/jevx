package main

import "testing"

// TestSamplePayloadUsesThePluginsTool: `plugin test` must build its sample for the tool the plugin watches. It always
// built a Bash command, so a Write|Edit or WebFetch plugin printed "would not run" and could not be tested at all.
func TestSamplePayloadUsesThePluginsTool(t *testing.T) {
	for _, c := range []struct{ event, tools, tool, field string }{
		{"PreToolUse", "Bash", "Bash", "command"},
		{"PreToolUse", "Write|Edit", "Write", "content"},
		{"PreToolUse", "Edit", "Edit", "new_string"},
		{"PostToolUse", "WebFetch|WebSearch", "WebFetch", "url"},
		{"PreToolUse", "", "Bash", "command"},
		{"PreToolUse", "mcp__.*", "Bash", "command"}, // a pattern, not a name: fall back to Bash
	} {
		p := samplePayload(c.event, c.tools, []string{"hello"})
		if p["tool_name"] != c.tool {
			t.Errorf("%s %q: tool %v, want %s", c.event, c.tools, p["tool_name"], c.tool)
			continue
		}
		in, _ := p["tool_input"].(map[string]any)
		if _, ok := in[c.field]; !ok {
			t.Errorf("%s %q: tool_input %v has no %q", c.event, c.tools, in, c.field)
		}
	}
	if r, _ := samplePayload("PostToolUse", "WebFetch", []string{"page text"})["tool_response"].(map[string]any); r["result"] != "page text" {
		t.Errorf("a WebFetch sample must carry the page as the tool's response: %v", r)
	}
}
