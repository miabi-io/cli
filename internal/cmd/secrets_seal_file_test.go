package cmd

import (
	"strings"
	"testing"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/miabi/pkg/sealed"
	"gopkg.in/yaml.v3"
)

const manifest = `# vault entries for the shop
apiVersion: miabi.io/v1
kind: Secret
metadata:
  name: stripe-key
spec:
  value: sk_live_123 # rotated monthly
---
apiVersion: miabi.io/v1
kind: Secret
metadata:
  name: session
spec:
  generate: true
  length: 48
---
apiVersion: miabi.io/v1
kind: Project
metadata:
  name: shop
spec:
  resources:
    - apiVersion: miabi.io/v1
      kind: Secret
      metadata:
        name: db-password
      spec:
        value: hunter2
---
apiVersion: miabi.io/v1
kind: Application
metadata:
  name: api
spec:
  image: nginx
`

func TestSealManifestsConvertsOnlyPlaintextSecrets(t *testing.T) {
	id, rcpt, err := sealed.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fetched := 0
	out, names, err := sealManifests([]byte(manifest), func() (*api.SealingKey, error) {
		fetched++
		return &api.SealingKey{Version: 3, PublicKey: rcpt}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "stripe-key,db-password" {
		t.Fatalf("sealed %v", names)
	}
	if fetched != 2 {
		t.Fatalf("key resolver called %d times", fetched)
	}
	text := string(out)
	for _, want := range []string{"# vault entries for the shop", "# rotated monthly", "generate: true", "kind: Application"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lost %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sk_live_123") || strings.Contains(text, "hunter2") {
		t.Fatalf("plaintext left in the output:\n%s", text)
	}

	dec := yaml.NewDecoder(strings.NewReader(text))
	opened := map[string]string{}
	for {
		var doc struct {
			Kind     string
			Metadata struct{ Name string }
			Spec     struct {
				Value     string
				Resources []struct {
					Kind     string
					Metadata struct{ Name string }
					Spec     struct{ Value string }
				}
			}
		}
		if dec.Decode(&doc) != nil {
			break
		}
		check := func(kind, name, value string) {
			if kind != "SealedSecret" {
				return
			}
			v, _, err := sealed.Open(value, name, id)
			if err != nil {
				t.Fatalf("%s does not open: %v", name, err)
			}
			opened[name] = v
		}
		check(doc.Kind, doc.Metadata.Name, doc.Spec.Value)
		for _, r := range doc.Spec.Resources {
			check(r.Kind, r.Metadata.Name, r.Spec.Value)
		}
	}
	if opened["stripe-key"] != "sk_live_123" || opened["db-password"] != "hunter2" {
		t.Fatalf("opened = %v", opened)
	}
}

func TestSealManifestsLeavesAFileWithoutPlaintextUntouched(t *testing.T) {
	src := "kind: Secret\nmetadata: { name: s }\nspec: { generate: true }\n"
	out, names, err := sealManifests([]byte(src), func() (*api.SealingKey, error) {
		t.Fatal("no key should be needed")
		return nil, nil
	})
	if err != nil || len(names) != 0 || string(out) != src {
		t.Fatalf("out = %q, names = %v, err = %v", out, names, err)
	}
}
