package cmd

import (
	"context"
	"os"

	"github.com/miabi-io/cli/internal/mcp"
	"github.com/spf13/cobra"
)

var (
	mcpAllowWrite bool
	mcpHTTP       string
)

func init() {
	f := mcpCmd.Flags()
	f.BoolVar(&mcpAllowWrite, "allow-write", false,
		"enable mutating tools (deploy, restart, rollback, …); default: read-only")
	f.StringVar(&mcpHTTP, "http", "",
		"serve over HTTP at this address instead of stdio (e.g. 127.0.0.1:8765)")
	rootCmd.AddCommand(mcpCmd)
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run a Model Context Protocol server for AI agents",
	Long: "Expose this Miabi panel to an AI agent (Claude Desktop, Claude Code, Cursor, …)\n" +
		"over the Model Context Protocol. The server turns each tool call into one\n" +
		"authenticated request to the panel's /api/v1 — so the agent inherits your token,\n" +
		"workspace, and RBAC. No model runs here.\n\n" +
		"It exposes tools (inspect apps, deployments, releases, databases, secret names),\n" +
		"resources (apps & deployments as miabi:// URIs the agent can attach), and prompts\n" +
		"(ready-made diagnostics). It is read-only by default; pass --allow-write to also\n" +
		"expose mutating tools (deploy, restart, rollback). Secret values are never returned.\n\n" +
		"Transport is stdio by default; pass --http to serve over HTTP instead.",
	Example: "  # Register with Claude Code (read-only):\n" +
		"  claude mcp add miabi -- miabi mcp\n\n" +
		"  # Allow the agent to deploy and restart:\n" +
		"  claude mcp add miabi -- miabi mcp --allow-write\n\n" +
		"  # Serve over HTTP on a loopback port instead of stdio:\n" +
		"  miabi mcp --http 127.0.0.1:8765\n\n" +
		"  # In a Claude Desktop config, the command is `miabi` with args [\"mcp\"].",
	Args: cobra.NoArgs,
	// The stdio protocol owns stdout; keep cobra's usage/errors off it.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		// Best-effort: resolve the active workspace once so tool calls that omit a
		// workspace argument have a default. A failure here is non-fatal — tools
		// can still take an explicit workspace.
		fallbackWS, _ := workspaceRef(ctx, c, eff)

		srv := mcp.New(mcp.Options{
			Client:            c,
			FallbackWorkspace: fallbackWS,
			AllowWrite:        mcpAllowWrite,
			Version:           version,
			Logf: func(format string, args ...any) {
				cmd.PrintErrf(format+"\n", args...)
			},
		})
		if mcpHTTP != "" {
			return srv.ServeHTTP(ctx, mcpHTTP)
		}
		return srv.Serve(ctx, os.Stdin, os.Stdout)
	},
}
