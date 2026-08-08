package cmd

import (
	"fmt"

	"github.com/miabi-io/miabi-cli/internal/ui"
)

// cliUI renders stackcmd's output the way the rest of the CLI looks — colored, with the same
// symbols as every other command. The server image implements the same interface with plain fmt,
// which is the only thing that should differ between the two front-ends.
type cliUI struct{}

func (cliUI) Printf(format string, a ...any)  { fmt.Printf(format, a...) }
func (cliUI) Info(format string, a ...any)    { ui.Info(format, a...) }
func (cliUI) Success(format string, a ...any) { ui.Success(format, a...) }
func (cliUI) Warn(format string, a ...any)    { ui.Warn(format, a...) }
func (cliUI) Confirm(prompt string) bool      { return ui.Confirm(prompt) }
