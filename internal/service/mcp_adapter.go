package service

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
)

// This file adapts the service's argument-only handler style to the official
// MCP Go SDK (github.com/modelcontextprotocol/go-sdk). Tool classes,
// behavior annotations and the serve-mode policy live in
// internal/domain/services/toolpolicy and internal/adapters/mcp.

// newTextContent builds a text content block for tool and prompt results.
func newTextContent(text string) mcp.Content {
	return &mcp.TextContent{Text: text}
}

// newToolResponse builds a tool result from one or more content blocks.
func newToolResponse(content ...mcp.Content) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: content}
}

// addTool registers a typed, argument-only handler through the tool-class
// registrar (internal/adapters/mcp). The registrar decides, from the tool's
// class in toolpolicy.DefaultCatalog and the serve-mode policy, whether the
// tool is advertised at all; the input schema is inferred from the In struct
// (json + jsonschema tags), inputs are validated by the SDK before the
// handler runs, and handler errors are returned as tool execution errors
// (isError) per the MCP spec.
func addTool[In any](r *mcpadapter.Registrar, name, description string, h func(In) (*mcp.CallToolResult, error)) {
	mcpadapter.AddToolLegacy(r, name, description, h)
}

// contentText returns the text of a content block, or "" if it is not text.
func contentText(c mcp.Content) string {
	if tc, ok := c.(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// promptResult builds a single-message prompt result attributed to the assistant.
func promptResult(description string, content mcp.Content) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages:    []*mcp.PromptMessage{{Role: "assistant", Content: content}},
	}
}
