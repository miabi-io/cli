// Command miabi is the imperative client for a Miabi control panel: it drives
// the deploy flow from a terminal or CI against the documented /api/v1 HTTP API.
// It imports nothing from the server; the public API is the only contract.
package main

import "github.com/miabi-io/cli/internal/cmd"

func main() { cmd.Execute() }
