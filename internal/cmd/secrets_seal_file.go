package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/miabi-io/miabi/pkg/sealed"
	"gopkg.in/yaml.v3"
)

// sealManifestFile converts the plaintext Secrets of a manifest file into SealedSecrets and writes the result
// back (inPlace) or to stdout. The sealing key is fetched only when there is something to seal.
func sealManifestFile(ctx context.Context, path string, inPlace bool) error {
	if inPlace && path == "-" {
		return errors.New("--in-place needs a file, not stdin")
	}
	var (
		src []byte
		err error
	)
	if path == "-" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	var key *api.SealingKey
	out, names, err := sealManifests(src, func() (*api.SealingKey, error) {
		if key == nil {
			k, err := resolveSealingKey(ctx)
			if err != nil {
				return nil, err
			}
			key = k
		}
		return key, nil
	})
	if err != nil {
		return err
	}
	if len(names) == 0 {
		ui.Info("No Secret with a plaintext value in %s; nothing to seal.", path)
		if !inPlace {
			_, err = os.Stdout.Write(src)
		}
		return err
	}
	if !inPlace {
		_, err = os.Stdout.Write(out)
		fmt.Fprintf(os.Stderr, "Sealed %d secret(s): %s\n", len(names), strings.Join(names, ", "))
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
		return err
	}
	ui.Success("Sealed %d secret(s) in %s: %s", len(names), path, strings.Join(names, ", "))
	return nil
}

// sealManifests rewrites every kind: Secret carrying a plaintext value — at the top level or inside a
// Project's spec.resources — as a SealedSecret of the same name, keeping the rest of each document.
func sealManifests(src []byte, key func() (*api.SealingKey, error)) ([]byte, []string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("parse manifests: %w", err)
		}
		docs = append(docs, &doc)
	}
	var names []string
	for _, doc := range docs {
		if len(doc.Content) == 0 {
			continue
		}
		if err := sealNode(doc.Content[0], key, &names); err != nil {
			return nil, nil, err
		}
	}
	if len(names) == 0 {
		return src, nil, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return nil, nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), names, nil
}

func sealNode(n *yaml.Node, key func() (*api.SealingKey, error), names *[]string) error {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	kind := mapValue(n, "kind")
	spec := mapValue(n, "spec")
	if kind == nil || spec == nil || spec.Kind != yaml.MappingNode {
		return nil
	}
	switch kind.Value {
	case "Project":
		if res := mapValue(spec, "resources"); res != nil && res.Kind == yaml.SequenceNode {
			for _, child := range res.Content {
				if err := sealNode(child, key, names); err != nil {
					return err
				}
			}
		}
	case "Secret":
		value := mapValue(spec, "value")
		if value == nil || value.Kind != yaml.ScalarNode || value.Value == "" {
			return nil // generated, or set out of band: there is no plaintext to seal
		}
		meta := mapValue(n, "metadata")
		var name *yaml.Node
		if meta != nil {
			name = mapValue(meta, "name")
		}
		if name == nil || name.Value == "" {
			return errors.New("a Secret with a value has no metadata.name; cannot seal it")
		}
		k, err := key()
		if err != nil {
			return err
		}
		v, err := sealed.Seal(k.PublicKey, k.Version, name.Value, value.Value)
		if err != nil {
			return fmt.Errorf("seal %s: %w", name.Value, err)
		}
		kind.Value = "SealedSecret"
		// A SealedSecret takes only value: the generation options of a Secret would be refused.
		valueKey := keyNode(spec, "value")
		spec.Content = []*yaml.Node{valueKey, {Kind: yaml.ScalarNode, Tag: "!!str", Value: v, LineComment: value.LineComment}}
		*names = append(*names, name.Value)
	}
	return nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func keyNode(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}
