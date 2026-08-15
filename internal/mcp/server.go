package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/miabi-io/cli/internal/api"
)

// Server serves MCP over a stdio pair or HTTP. It holds one API client and a
// resolved fallback workspace (the CLI's active/bound workspace, used when a
// tool or resource omits a workspace).
type Server struct {
	client     *api.Client
	fallbackWS string
	allowWrite bool
	version    string

	tools   map[string]tool
	order   []string // tool names in registration order (stable tools/list output)
	prompts []prompt // registration order

	enc   *json.Encoder
	encMu sync.Mutex // serializes stdio writes
	logf  func(format string, args ...any)
}

// Options configures a Server.
type Options struct {
	Client *api.Client
	// FallbackWorkspace is the workspace name used when a call omits the
	// "workspace" argument (typically the CLI's active/bound workspace). May be
	// empty, in which case calls require an explicit workspace unless the token
	// can see exactly one.
	FallbackWorkspace string
	// AllowWrite enables the mutating tools (deploy, restart, rollback, …). When
	// false the server is strictly read-only and those tools are not advertised.
	AllowWrite bool
	Version    string
	// Logf sinks diagnostics. On stdio the protocol owns stdout, so logs must go
	// to stderr (or nowhere); never to stdout.
	Logf func(format string, args ...any)
}

// New builds a Server with the standard tool and prompt catalogs registered.
func New(o Options) *Server {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		client:     o.Client,
		fallbackWS: o.FallbackWorkspace,
		allowWrite: o.AllowWrite,
		version:    o.Version,
		tools:      map[string]tool{},
		logf:       logf,
	}
	s.registerTools()
	s.registerPrompts()
	return s
}

// Serve runs the stdio read-dispatch-reply loop until in reaches EOF or ctx is
// cancelled. Each JSON value on in is one JSON-RPC message; each reply is one on
// out. Returns nil on a clean EOF (the client closed the pipe).
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.enc = json.NewEncoder(out)
	// A decoder reads successive JSON values regardless of the newline framing,
	// which tolerates both pretty- and compact-printed clients.
	dec := json.NewDecoder(bufio.NewReaderSize(in, 1<<20))

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A malformed message is unrecoverable on a stream decoder (we can't
			// find the next boundary), so report and stop.
			s.writeResp(respondErr(nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()}))
			return nil
		}
		if resp := s.handle(ctx, req); resp != nil {
			s.writeResp(resp)
		}
	}
}

// handle routes one request to its handler and returns the reply, or nil when the
// request is a notification (which gets none). It is transport-agnostic: both the
// stdio loop and the HTTP handler call it.
func (s *Server) handle(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return respondOK(req.ID, s.handleInitialize())
	case "notifications/initialized", "notifications/cancelled":
		return nil // client-side lifecycle notifications; nothing to do
	case "ping":
		return respondOK(req.ID, map[string]any{})
	case "tools/list":
		return respondOK(req.ID, toolsListResult{Tools: s.toolDefs()})
	case "tools/call":
		res, rerr := s.handleToolCall(ctx, req.Params)
		if rerr != nil {
			return respondErr(req.ID, rerr)
		}
		return respondOK(req.ID, res)
	case "resources/list":
		res, rerr := s.handleResourcesList(ctx)
		if rerr != nil {
			return respondErr(req.ID, rerr)
		}
		return respondOK(req.ID, res)
	case "resources/templates/list":
		return respondOK(req.ID, resourceTemplatesListResult{ResourceTemplates: resourceTemplates()})
	case "resources/read":
		res, rerr := s.handleResourceRead(ctx, req.Params)
		if rerr != nil {
			return respondErr(req.ID, rerr)
		}
		return respondOK(req.ID, res)
	case "prompts/list":
		return respondOK(req.ID, promptsListResult{Prompts: s.promptDefs()})
	case "prompts/get":
		res, rerr := s.handlePromptGet(req.Params)
		if rerr != nil {
			return respondErr(req.ID, rerr)
		}
		return respondOK(req.ID, res)
	default:
		if req.isNotification() {
			return nil // unknown notifications are ignored per JSON-RPC
		}
		return respondErr(req.ID, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method})
	}
}

func (s *Server) handleInitialize() initializeResult {
	mode := "read-only"
	if s.allowWrite {
		mode = "read-write"
	}
	return initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities: map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		ServerInfo: serverInfo{Name: "miabi", Version: s.version},
		Instructions: fmt.Sprintf(
			"Tools operate on a Miabi control panel over its API (%s). Most tools take an "+
				"optional \"workspace\"; when omitted, the CLI's active workspace is used. "+
				"Secret values are never returned. Apps and deployments are also exposed as "+
				"miabi:// resources, and the prompts offer ready-made diagnostics.",
			mode),
	}
}

// handleToolCall decodes the params, dispatches to the named tool, and wraps the
// result. A tool-level error (bad args, API failure) is returned inside a
// successful result with IsError=true, so the agent can read and react to it;
// only a malformed request yields an rpcError.
func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (*callToolResult, *rpcError) {
	var p callToolParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	t, ok := s.tools[p.Name]
	if !ok {
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool: " + p.Name}
	}
	// Defense in depth: a write tool must never run in read-only mode even if it
	// somehow appeared in the catalog.
	if !t.readOnly && !s.allowWrite {
		return &callToolResult{Content: textContent("tool \"" + p.Name + "\" is disabled: start the server with --allow-write to enable mutating tools"), IsError: true}, nil
	}
	out, err := t.handler(ctx, s, p.Arguments)
	if err != nil {
		return &callToolResult{Content: textContent(err.Error()), IsError: true}, nil
	}
	return &callToolResult{Content: textContent(jsonString(out))}, nil
}

// respondOK builds a success response, or nil for a notification (no id).
func respondOK(id json.RawMessage, result any) *rpcResponse {
	if id == nil {
		return nil
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// respondErr builds an error response. Unlike respondOK it is emitted even for a
// nil id, so a client waiting on a top-level failure (e.g. parse error) isn't
// left hanging.
func respondErr(id json.RawMessage, rerr *rpcError) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: rerr}
}

// writeResp emits one response on the stdio encoder under the write lock.
func (s *Server) writeResp(resp *rpcResponse) {
	if resp == nil {
		return
	}
	s.encMu.Lock()
	defer s.encMu.Unlock()
	if err := s.enc.Encode(resp); err != nil {
		s.logf("mcp: write reply: %v", err)
	}
}

// jsonString renders v as indented JSON for a text content block, falling back
// to a Go representation if it somehow can't be marshalled.
func jsonString(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
