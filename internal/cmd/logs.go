package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	logsDeployment int
	logsFollow     bool
	logsTail       int
)

func init() {
	f := logsCmd.Flags()
	f.IntVar(&logsDeployment, "deployment", 0, "show a deployment's build/deploy logs by its number instead of runtime logs")
	f.BoolVarP(&logsFollow, "follow", "f", false, "stream new logs (default: print the current logs and exit)")
	f.IntVar(&logsTail, "tail", 200, "number of trailing lines to show first (runtime logs)")
	logsCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs [app] [--follow] [--tail N] [--deployment <number>]",
	Short: "Show an application's logs (or a deployment's build logs)",
	Long: "Prints the running container's current logs and exits; pass --follow to stream\n" +
		"new output. Pass --deployment <number> to instead show that deployment's\n" +
		"build/deploy logs (numbers come from `miabi apps deployments`). The app is\n" +
		"positional or the one bound by `miabi use`.",
	Example: "  miabi apps logs web                 # current logs, then exit\n" +
		"  miabi apps logs web --follow        # stream new logs\n" +
		"  miabi apps logs web --deployment 7  # build logs of deploy #7",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		appID, _, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}
		base := strings.TrimRight(eff.URL, "/")

		// Deployment build/deploy logs — addressed by the per-app number.
		if logsDeployment > 0 {
			dep, err := c.DeploymentByNumber(ctx, ws, appID, logsDeployment)
			if err != nil {
				return err
			}
			url := fmt.Sprintf("%s/api/v1/workspaces/%s/apps/%d/deployments/%d/logs", base, ws, appID, dep.ID)
			return streamDeployLogs(ctx, url, eff.Token)
		}

		// Runtime container logs (default).
		follow := logsFollow
		url := fmt.Sprintf("%s/api/v1/workspaces/%s/apps/%d/logs/stream?tail=%d&follow=%t", base, ws, appID, logsTail, follow)
		return streamRuntimeLogs(ctx, url, eff.Token)
	},
}

// newSSERequest builds an authenticated GET for an SSE endpoint.
func newSSERequest(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	return http.DefaultClient.Do(req)
}

// scanSSE calls onData for each SSE `data:` payload on the response body.
func scanSSE(resp *http.Response, onData func([]byte)) error {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		onData([]byte(strings.TrimSpace(data)))
	}
	return sc.Err()
}

// deployEvent mirrors the deployment stream payload: {"Type":"log|status","Data":"…"}.
type deployEvent struct {
	Type string `json:"Type"`
	Data string `json:"Data"`
}

// streamDeployLogs reads the deployment-log SSE stream, printing log lines and
// exiting when the deployment reaches a terminal status (failed → non-zero exit).
func streamDeployLogs(ctx context.Context, url, token string) error {
	resp, err := newSSERequest(ctx, url, token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logs stream: HTTP %d", resp.StatusCode)
	}
	var failed bool
	serr := scanSSE(resp, func(data []byte) {
		var ev deployEvent
		if json.Unmarshal(data, &ev) != nil {
			return
		}
		switch ev.Type {
		case "log":
			fmt.Println(ev.Data)
		case "status":
			ui.Info("status: %s", ui.Status(ev.Data))
			if api.IsFailure(ev.Data) {
				failed = true
			}
		}
	})
	if serr != nil {
		return serr
	}
	if failed {
		return fmt.Errorf("deployment failed")
	}
	return nil
}

// runtimeLine mirrors the runtime log payload: {"stream":"stdout|stderr","text":"…"}.
type runtimeLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// streamRuntimeLogs reads the runtime container-log SSE stream, printing each
// line (stderr dimmed) until the stream ends or the context is cancelled.
func streamRuntimeLogs(ctx context.Context, url, token string) error {
	resp, err := newSSERequest(ctx, url, token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("no running container for this app — deploy it first, or check `miabi apps status`")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logs stream: HTTP %d", resp.StatusCode)
	}
	return scanSSE(resp, func(data []byte) {
		var l runtimeLine
		if json.Unmarshal(data, &l) != nil {
			return
		}
		if l.Stream == "stderr" {
			fmt.Println(ui.Dim(l.Text))
			return
		}
		fmt.Println(l.Text)
	})
}
