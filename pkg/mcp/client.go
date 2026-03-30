package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	servermcp "github.com/mark3labs/mcp-go/mcp"
)

type TransportKind string

const (
	TransportHTTP  TransportKind = "http"
	TransportStdio TransportKind = "stdio"
)

type Options struct {
	Kind TransportKind

	// HTTP
	HTTPBaseURL string

	// Stdio
	Command string
	Args    []string
	Env     []string
	WorkDir string
}

type Client struct {
	inner  *mcpclient.Client
	stderr io.Reader
}

func Connect(ctx context.Context, opt Options) (*Client, error) {
	kind := opt.Kind
	if kind == "" {
		kind = TransportHTTP
	}

	switch kind {
	case TransportHTTP:
		if strings.TrimSpace(opt.HTTPBaseURL) == "" {
			return nil, fmt.Errorf("HTTPBaseURL is required")
		}
		c, err := mcpclient.NewStreamableHttpClient(strings.TrimSpace(opt.HTTPBaseURL))
		if err != nil {
			return nil, err
		}
		if err := c.Start(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return &Client{inner: c}, nil
	case TransportStdio:
		cmd := strings.TrimSpace(opt.Command)
		if cmd == "" {
			return nil, fmt.Errorf("Command is required")
		}

		c, err := mcpclient.NewStdioMCPClientWithOptions(
			cmd,
			opt.Env,
			opt.Args,
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				execCmd := exec.CommandContext(ctx, command, args...)
				execCmd.Env = append(os.Environ(), env...)
				if strings.TrimSpace(opt.WorkDir) != "" {
					execCmd.Dir = opt.WorkDir
				}
				return execCmd, nil
			}),
		)
		if err != nil {
			return nil, err
		}

		var stderr io.Reader
		if r, ok := mcpclient.GetStderr(c); ok {
			stderr = r
		}
		return &Client{inner: c, stderr: stderr}, nil
	default:
		return nil, fmt.Errorf("unsupported transport kind: %s", kind)
	}
}

func (c *Client) Stderr() io.Reader {
	return c.stderr
}

func (c *Client) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *Client) Initialize(ctx context.Context, clientName, clientVersion string) (*servermcp.InitializeResult, error) {
	if c == nil || c.inner == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if strings.TrimSpace(clientName) == "" {
		clientName = "Ling"
	}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = "0.0.0"
	}

	return c.inner.Initialize(ctx, servermcp.InitializeRequest{Params: servermcp.InitializeParams{
		ProtocolVersion: servermcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      servermcp.Implementation{Name: clientName, Version: clientVersion},
		Capabilities:    servermcp.ClientCapabilities{},
	}})
}

func (c *Client) ListTools(ctx context.Context) (*servermcp.ListToolsResult, error) {
	if c == nil || c.inner == nil {
		return nil, fmt.Errorf("client is nil")
	}
	return c.inner.ListTools(ctx, servermcp.ListToolsRequest{})
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*servermcp.CallToolResult, error) {
	if c == nil || c.inner == nil {
		return nil, fmt.Errorf("client is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if args == nil {
		args = map[string]any{}
	}
	return c.inner.CallTool(ctx, servermcp.CallToolRequest{Params: servermcp.CallToolParams{Name: name, Arguments: args}})
}
