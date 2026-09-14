package fcm

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const uiFormPath = "../../../../ui/src/components/workflow/Sink/FcmSinkConfig.tsx"

var updateConfigRE = regexp.MustCompile(`updateConfig\(\s*'([a-z0-9_]+)'`)

// aliasKeys are read by FromMap but deliberately not offered by the form: they
// are second names for a field the form already has, kept because a sink saved
// through the REST API may use them.
var aliasKeys = []string{"token"}

// batchKey is read by the factory rather than FromMap — it decides which
// constructor to call, not what the sink is configured with.
const batchKey = "batch"

// TestUIFormMatchesConfigKeys is the gate that keeps the editor's FCM form and
// the sink's configuration in step.
//
// The form cannot import the Go parser, so it restates the key names. A
// restated name drifts in both directions and neither is loud: a field written
// under a name nothing reads is a setting the operator fills in and that never
// takes effect, and a key the sink reads with no field is a capability nobody
// can reach from the UI. Twelve sink types once rendered the wrong form
// entirely and nothing failed.
func TestUIFormMatchesConfigKeys(t *testing.T) {
	src, err := os.ReadFile(filepath.Clean(uiFormPath))
	if err != nil {
		t.Fatalf("cannot read the editor's FCM form at %s: %v", uiFormPath, err)
	}

	matches := updateConfigRE.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatalf("no updateConfig calls found in %s; has the form been reformatted?", uiFormPath)
	}

	written := map[string]bool{}
	for _, m := range matches {
		written[m[1]] = true
	}

	read := ConfigKeys()

	for key := range written {
		if key == batchKey {
			continue
		}
		if !slices.Contains(read, key) {
			t.Errorf("the form writes %q, which FromMap does not read: the operator fills that field in and nothing happens", key)
		}
	}

	for _, key := range read {
		if slices.Contains(aliasKeys, key) {
			continue
		}
		if !written[key] {
			t.Errorf("FromMap reads %q and no field in the form writes it: the capability exists but nobody can reach it from the UI", key)
		}
	}
}

// TestConfigKeysAreLive guards the list itself: a key recorded as read but
// wired to nothing would satisfy the parity test above while doing nothing.
func TestConfigKeysAreLive(t *testing.T) {
	// A value that is accepted by every parser this config uses.
	const probe = "1"

	for _, key := range ConfigKeys() {
		if key == "credentials_json" || key == "project_id" {
			// Both are plain strings whose effect is checked by resolveProject.
			continue
		}
		t.Run(key, func(t *testing.T) {
			value := probe
			switch {
			case strings.HasSuffix(key, "_ttl"), strings.HasSuffix(key, "_expiration"), key == "timeout":
				value = "30s"
			case key == "data_mode":
				value = string(DataFields)
			case key == "on_oversize":
				value = string(OversizeDrop)
			case key == "action":
				value = string(ActionSubscribe)
			case key == "android_priority":
				value = "high"
			case key == "android_notification_priority":
				value = "max"
			case key == "apns_priority":
				value = "10"
			case key == "data_json":
				value = `{"k":"v"}`
			case strings.HasPrefix(key, "use_"), strings.HasPrefix(key, "dry_"),
				key == "apns_content_available", key == "apns_mutable_content":
				value = "true"
			}

			cfg, err := FromMap(map[string]string{key: value})
			if err != nil {
				t.Fatalf("FromMap(%s=%q): %v", key, value, err)
			}
			empty, _ := FromMap(nil)
			if equalConfigs(cfg, empty) {
				t.Errorf("setting %q changed nothing in the resulting Config; the key is read and then dropped", key)
			}
		})
	}
}

// equalConfigs compares the fields FromMap can populate. reflect.DeepEqual is
// no use here: Config carries an http.Client and a Formatter, neither of which
// FromMap sets and neither of which compares meaningfully.
func equalConfigs(a, b Config) bool {
	a.Formatter, b.Formatter = nil, nil
	a.HTTPClient, b.HTTPClient = nil, nil
	if len(a.Data) != len(b.Data) {
		return false
	}
	for k, v := range a.Data {
		if b.Data[k] != v {
			return false
		}
	}
	a.Data, b.Data = nil, nil
	return reflect.DeepEqual(a, b)
}
