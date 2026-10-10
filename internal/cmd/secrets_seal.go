package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/miabi/pkg/sealed"
	"github.com/spf13/cobra"
)

var (
	sealPublicKey     string
	sealPublicKeyFile string
	sealRaw           bool
	sealFile          string
	sealInPlace       bool
)

func init() {
	sealCmd.Flags().StringVar(&secretValue, "value", "", "the secret value (leaks into shell history; prefer --from-file or stdin)")
	sealCmd.Flags().StringVar(&secretFromFile, "from-file", "", "read the value from a file (use \"-\" for stdin)")
	sealCmd.Flags().StringVar(&sealPublicKey, "public-key", "", "seal offline to this public key (age1…) instead of fetching the workspace's")
	sealCmd.Flags().StringVar(&sealPublicKeyFile, "public-key-file", "", "seal offline to the public key in this file, e.g. one saved with `miabi secrets seal-key`")
	sealCmd.Flags().BoolVar(&sealRaw, "raw", false, "print only the sealed value, not a SealedSecret manifest")
	sealCmd.Flags().StringVarP(&sealFile, "file", "f", "", "convert every Secret with a plaintext value in this manifest file into a SealedSecret")
	sealCmd.Flags().BoolVarP(&sealInPlace, "in-place", "i", false, "with -f, rewrite the file instead of printing the result")

	secretCmd.AddCommand(sealCmd, sealKeyCmd)
}

var sealCmd = &cobra.Command{
	Use:   "seal (<name> [--value V | --from-file F] [--raw] | -f FILE [--in-place]) [--public-key K | --public-key-file F]",
	Short: "Encrypt a secret value so it can be committed to git",
	Long: "Encrypts a value to the workspace's public sealing key and prints a\n" +
		"SealedSecret manifest. The result is safe to commit: only this workspace\n" +
		"can open it, and only for a SealedSecret with this name. Encryption\n" +
		"happens locally — the value is never sent to the server.\n\n" +
		"Without --public-key the key is fetched from the workspace (read access is\n" +
		"enough). Provide the value with --value, --from-file, or by piping it on stdin.\n\n" +
		"With -f, every kind: Secret in the file that carries a plaintext value becomes\n" +
		"a SealedSecret of the same name, comments kept; generated Secrets are left as\n" +
		"they are. Applying the result takes over the existing vault entries in place.",
	Example: "  echo -n \"$STRIPE_KEY\" | miabi secrets seal stripe-key >> secrets.yaml\n" +
		"  miabi secrets seal db-password --from-file pw.txt --raw\n" +
		"  miabi secrets seal-key > .miabi/sealing.pub\n" +
		"  miabi secrets seal api-token --from-file token.txt --public-key-file .miabi/sealing.pub\n" +
		"  miabi secrets seal -f secrets.yaml --in-place",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if sealFile != "" {
			if len(args) > 0 {
				return errors.New("pass either a secret name or -f, not both")
			}
			return sealManifestFile(cmd.Context(), sealFile, sealInPlace)
		}
		if sealInPlace {
			return errors.New("--in-place needs -f FILE")
		}
		if len(args) == 0 {
			return errors.New("a secret name is required (or -f FILE to seal a manifest)")
		}
		name := args[0]
		value, ok, err := readSecretValue()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("no value: pass --value, --from-file, or pipe it on stdin")
		}
		key, err := resolveSealingKey(cmd.Context())
		if err != nil {
			return err
		}
		v, err := sealed.Seal(key.PublicKey, key.Version, name, value)
		if err != nil {
			return err
		}
		if sealRaw {
			fmt.Println(v)
			return nil
		}
		fmt.Printf("---\napiVersion: miabi.io/v1\nkind: SealedSecret\nmetadata:\n  name: %s\nspec:\n  value: %s\n", name, v)
		return nil
	},
}

var sealKeyCmd = &cobra.Command{
	Use:   "seal-key",
	Short: "Print the workspace's public sealing key",
	Long: "Prints the public key secrets are sealed to. It is not a secret: commit it\n" +
		"next to your manifests so CI or teammates can seal values offline with\n" +
		"`miabi secrets seal --public-key-file`.",
	Example: "  miabi secrets seal-key > .miabi/sealing.pub",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		key, err := fetchSealingKey(cmd.Context())
		if err != nil {
			return err
		}
		if structured() {
			return emit(key)
		}
		fmt.Println(key.PublicKey)
		return nil
	},
}

// resolveSealingKey prefers an offline key; its version is unknown, so the server tries every key it holds.
func resolveSealingKey(ctx context.Context) (*api.SealingKey, error) {
	pub := strings.TrimSpace(sealPublicKey)
	if pub == "" && sealPublicKeyFile != "" {
		b, err := os.ReadFile(sealPublicKeyFile)
		if err != nil {
			return nil, err
		}
		pub = strings.TrimSpace(string(b))
	}
	if pub == "" {
		return fetchSealingKey(ctx)
	}
	if !sealed.ValidRecipient(pub) {
		return nil, fmt.Errorf("%q is not a sealing public key (expected age1…)", pub)
	}
	return &api.SealingKey{PublicKey: pub}, nil
}

func fetchSealingKey(ctx context.Context) (*api.SealingKey, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c, eff, err := newClient()
	if err != nil {
		return nil, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, err
	}
	return c.SealingKey(ctx, ws)
}
