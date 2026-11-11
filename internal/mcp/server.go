// Copyright (c) 2026 Jonas Kaninda.
//
// This file is part of Miabi.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// Server serves MCP over stdio or HTTP against one API client.
type Server struct {
	client     *api.Client
	fallbackWS string
	allowWrite bool
	version    string

	tools   map[string]tool
	order   []string // keeps tools/list output stable
	prompts []prompt

	enc   *json.Encoder
	encMu sync.Mutex // serializes stdio writes
	logf  func(format string, args ...any)
}

type Options struct {
	Client *api.Client
	// FallbackWorkspace may be empty; calls then need a workspace unless the token sees exactly one.
	FallbackWorkspace string
	// Without AllowWrite the mutating tools are not even advertised.
	AllowWrite bool
	Version    string
	// Logf must never write to stdout: on stdio the protocol owns it.
	Logf func(format string, args ...any)
}

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

// Serve returns nil on a clean EOF, when the client closed the pipe.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.enc = json.NewEncoder(out)
	// A decoder rather than line framing tolerates both pretty- and compact-printed clients.
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
			// A stream decoder can't find the next boundary after malformed input, so stop.
			s.writeResp(respondErr(nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()}))
			return nil
		}
		if resp := s.handle(ctx, req); resp != nil {
			s.writeResp(resp)
		}
	}
}

// handle returns nil for notifications; both the stdio and HTTP transports call it.
func (s *Server) handle(ctx context.Context, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return respondOK(req.ID, s.handleInitialize())
	case "notifications/initialized", "notifications/cancelled":
		return nil
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

// handleToolCall returns tool errors inside a successful result so the agent can react to them.
func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (*callToolResult, *rpcError) {
	var p callToolParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	t, ok := s.tools[p.Name]
	if !ok {
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool: " + p.Name}
	}
	// Defense in depth: never run a write tool in read-only mode, even if it reached the catalog.
	if !t.readOnly && !s.allowWrite {
		return &callToolResult{Content: textContent("tool \"" + p.Name + "\" is disabled: start the server with --allow-write to enable mutating tools"), IsError: true}, nil
	}
	out, err := t.handler(ctx, s, p.Arguments)
	if err != nil {
		return &callToolResult{Content: textContent(err.Error()), IsError: true}, nil
	}
	return &callToolResult{Content: textContent(jsonString(out))}, nil
}

// respondOK returns nil for a notification (no id).
func respondOK(id json.RawMessage, result any) *rpcResponse {
	if id == nil {
		return nil
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// respondErr replies even for a nil id so a client isn't left hanging on a parse error.
func respondErr(id json.RawMessage, rerr *rpcError) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: rerr}
}

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

func jsonString(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
