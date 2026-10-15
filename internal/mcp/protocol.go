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

// Package mcp is a Model Context Protocol server that exposes a Miabi control
// panel to AI agents (Claude Desktop, Claude Code, Cursor, …). It is a thin
// protocol wrapper over internal/api: every tool call is one authenticated
// request to the panel's public /api/v1, so the agent inherits the operator's
// token, workspace scoping, and RBAC. No model inference happens here.
//
// The server speaks JSON-RPC 2.0 over stdio (newline-delimited messages) or over
// the Streamable HTTP transport (see http.go). It implements the initialize
// handshake plus tools, resources, and prompts. Kept dependency-free — only the
// standard library — to match the CLI's thin footprint.
package mcp

import "encoding/json"

// protocolVersion is the MCP revision this server implements. Clients send their
// own in initialize; we echo a version we support.
const protocolVersion = "2025-06-18"

// JSON-RPC 2.0 standard error codes (see the spec). Application/tool errors are
// not transport errors — they are reported inside a successful tools/call result
// via isError, not as one of these.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// rpcRequest is an incoming JSON-RPC message. A missing ID marks a notification,
// to which the server sends no reply.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r rpcRequest) isNotification() bool { return len(r.ID) == 0 }

// rpcResponse is an outgoing reply. Exactly one of Result / Error is set.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// initializeResult answers the initialize handshake, advertising the tools,
// resources, and prompts capabilities.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolDef is one entry in the tools/list catalog. InputSchema is a JSON Schema
// object describing the tool's arguments; Annotations carry the read-only and
// destructive hints clients use to decide whether to prompt before calling.
type toolDef struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

type toolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// callToolParams is the body of a tools/call request.
type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// callToolResult is the result of tools/call. Content is a list of typed blocks;
// this server only emits text blocks (JSON-encoded data). IsError signals a
// tool-level failure (e.g. the API returned an error) without failing the RPC.
type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textContent(text string) []contentBlock {
	return []contentBlock{{Type: "text", Text: text}}
}

// --- resources ------------------------------------------------------------

// resourceDef is one entry in the resources/list catalog: a concrete, readable
// URI (an app or deployment) an agent can attach as context.
type resourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type resourcesListResult struct {
	Resources []resourceDef `json:"resources"`
}

// resourceTemplate is a parameterized URI (RFC 6570) an agent can fill in to read
// resources that aren't worth enumerating up front (every deployment of an app).
type resourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type resourceTemplatesListResult struct {
	ResourceTemplates []resourceTemplate `json:"resourceTemplates"`
}

type readResourceParams struct {
	URI string `json:"uri"`
}

type readResourceResult struct {
	Contents []resourceContents `json:"contents"`
}

type resourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// --- prompts --------------------------------------------------------------

// promptDef advertises one prompt in prompts/list.
type promptDef struct {
	Name        string      `json:"name"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Arguments   []promptArg `json:"arguments,omitempty"`
}

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type promptsListResult struct {
	Prompts []promptDef `json:"prompts"`
}

type getPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

type getPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []promptMessage `json:"messages"`
}

type promptMessage struct {
	Role    string       `json:"role"`
	Content contentBlock `json:"content"`
}
