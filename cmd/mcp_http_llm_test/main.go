package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/utils"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Printf("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL\n")
		return
	}

	mcpURL := strings.TrimSpace(utils.GetEnv("MCP_HTTP_URL"))
	if mcpURL == "" {
		mcpURL = "http://localhost:3000/mcp"
	}

	question := strings.TrimSpace(utils.GetEnv("QUESTION"))
	if question == "" {
		question = "请获取系统信息，并告诉我当前OS和CPU架构。"
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL, SystemPrompt: "你是一个工具调用助手。你必须在需要时选择工具，并严格输出JSON。"})
	if err != nil {
		fmt.Printf("llm init failed: %v\n", err)
		return
	}

	c, err := client.NewStreamableHttpClient(mcpURL)
	if err != nil {
		fmt.Printf("create http mcp client failed: %v\n", err)
		return
	}
	defer func() { _ = c.Close() }()

	if err := c.Start(ctx); err != nil {
		fmt.Printf("mcp client start failed: %v\n", err)
		return
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "Ling-MCP-HTTP-LLM-Test", Version: "0.0.1"},
		Capabilities:    mcp.ClientCapabilities{},
	}}); err != nil {
		fmt.Printf("mcp initialize failed: %v\n", err)
		return
	}

	toolsRes, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Printf("tools/list failed: %v\n", err)
		return
	}

	toolDesc := strings.Builder{}
	toolDesc.WriteString("可用工具：\n")
	for _, t := range toolsRes.Tools {
		toolDesc.WriteString("- ")
		toolDesc.WriteString(t.Name)
		if strings.TrimSpace(t.Description) != "" {
			toolDesc.WriteString(": ")
			toolDesc.WriteString(strings.TrimSpace(t.Description))
		}
		toolDesc.WriteString("\n")
	}

	selectPrompt := "你要根据用户问题选择一个工具并给出调用参数。\n" +
		"只输出JSON，不要解释，不要markdown。\n" +
		"JSON schema: {\"name\": string, \"arguments\": object}\n\n" +
		toolDesc.String() + "\n" +
		"用户问题：" + strings.TrimSpace(question) + "\n"

	callJSON, err := h.Query(selectPrompt, model)
	if err != nil {
		fmt.Printf("llm tool selection failed: %v\n", err)
		return
	}
	callJSON = strings.TrimSpace(callJSON)
	callJSON2 := extractJSON(callJSON)
	if strings.TrimSpace(callJSON2) != "" {
		callJSON = callJSON2
	}

	var tc toolCall
	if err := json.Unmarshal([]byte(callJSON), &tc); err != nil {
		fmt.Printf("parse tool call json failed: %v\nraw=%s\n", err, callJSON)
		return
	}
	if strings.TrimSpace(tc.Name) == "" {
		fmt.Printf("invalid tool call: empty name\n")
		return
	}
	if tc.Arguments == nil {
		tc.Arguments = map[string]any{}
	}

	fmt.Printf("Selected tool: %s args=%v\n", tc.Name, tc.Arguments)

	toolRes, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tc.Name, Arguments: tc.Arguments}})
	if err != nil {
		fmt.Printf("tools/call failed: %v\n", err)
		return
	}

	obsText := strings.Builder{}
	for _, content := range toolRes.Content {
		if t, ok := content.(mcp.TextContent); ok {
			obsText.WriteString(t.Text)
			obsText.WriteString("\n")
		}
	}
	observation := strings.TrimSpace(obsText.String())

	summarizePrompt := "用户问题：" + strings.TrimSpace(question) + "\n\n" +
		"工具返回：\n" + observation + "\n\n" +
		"请用简洁中文回答用户问题（不要提到JSON-RPC、MCP等实现细节）。"

	final, err := h.Query(summarizePrompt, model)
	if err != nil {
		fmt.Printf("llm summarize failed: %v\n", err)
		return
	}

	fmt.Printf("\nFinal answer:\n%s\n", strings.TrimSpace(final))
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "```json"); i >= 0 {
		s2 := s[i+len("```json"):]
		if j := strings.Index(s2, "```"); j >= 0 {
			return strings.TrimSpace(s2[:j])
		}
	}
	l := strings.Index(s, "{")
	r := strings.LastIndex(s, "}")
	if l >= 0 && r > l {
		return strings.TrimSpace(s[l : r+1])
	}
	return ""
}
