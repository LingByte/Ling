package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/utils"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	baseURL := strings.TrimSpace(utils.GetEnv("MCP_HTTP_URL"))
	if baseURL == "" {
		baseURL = "http://localhost:3000/mcp"
	}

	c, err := client.NewStreamableHttpClient(baseURL)
	if err != nil {
		fmt.Printf("create http mcp client failed: %v\n", err)
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.Start(ctx); err != nil {
		fmt.Printf("mcp client start failed: %v\n", err)
		return
	}

	_, err = c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "Ling-MCP-HTTP-Test", Version: "0.0.1"},
		Capabilities:    mcp.ClientCapabilities{},
	}})
	if err != nil {
		fmt.Printf("initialize failed: %v\n", err)
		return
	}

	toolsRes, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Printf("tools/list failed: %v\n", err)
		return
	}
	fmt.Printf("tools=%d\n", len(toolsRes.Tools))
	for _, t := range toolsRes.Tools {
		fmt.Printf("- %s: %s\n", t.Name, t.Description)
	}

	callRes, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "system_info",
		Arguments: map[string]any{"info_type": "basic"},
	}})
	if err != nil {
		fmt.Printf("tools/call system_info failed: %v\n", err)
		return
	}
	for _, c := range callRes.Content {
		if t, ok := c.(mcp.TextContent); ok {
			fmt.Printf("system_info:\n%s\n", t.Text)
		}
	}
}
