package files

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// SecretConfigKeys must name exactly the fields MaskSecrets hides: `config set` reads the list to
// keep from printing a secret back. Every string field is filled first, so a secret MaskSecrets
// learns to mask and the list doesn't name fails here.
func TestSecretConfigKeysMatchMaskSecrets(t *testing.T) {
	c := &Config{}
	v := reflect.ValueOf(c).Elem()
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.Kind() == reflect.String && f.CanSet() {
			f.SetString("value")
		}
	}
	raw, err := json.Marshal(c.MaskSecrets())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var masked []string
	for k, v := range fields {
		if v == SecretPlaceholder {
			masked = append(masked, k)
		}
	}
	slices.Sort(masked)
	want := slices.Clone(SecretConfigKeys)
	slices.Sort(want)
	if !slices.Equal(masked, want) {
		t.Errorf("MaskSecrets hides %v, SecretConfigKeys lists %v", masked, want)
	}
}
