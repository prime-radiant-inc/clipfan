package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigDocumentRevisionStates(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		state RevisionState
		rev   *uint64
	}{
		{
			name:  "pre v2",
			body:  `{"shared_key":"k","max_history":50}`,
			state: RevisionStatePreV2,
		},
		{
			name:  "v2 missing revision",
			body:  `{"config_version":2,"shared_key":"k","max_history":50}`,
			state: RevisionStateMissingRevision,
		},
		{
			name:  "v2 null revision",
			body:  `{"config_version":2,"config_revision":null,"shared_key":"k","max_history":50}`,
			state: RevisionStateMissingRevision,
		},
		{
			name:  "v2 versioned",
			body:  `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`,
			state: RevisionStateVersioned,
			rev:   uint64Ptr(7),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parseConfigDocument([]byte(tc.body))
			if err != nil {
				t.Fatalf("parseConfigDocument: %v", err)
			}
			if doc.RevisionState != tc.state {
				t.Fatalf("RevisionState = %q, want %q", doc.RevisionState, tc.state)
			}
			if !sameRevision(doc.ConfigRevision, tc.rev) {
				t.Fatalf("ConfigRevision = %v, want %v", revisionString(doc.ConfigRevision), revisionString(tc.rev))
			}
			if doc.Config.SharedKey != "k" {
				t.Fatalf("SharedKey = %q, want k", doc.Config.SharedKey)
			}
		})
	}
}

func TestParseConfigDocumentRejectsInvalidConfigVersion(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{"null", `{"config_version":null}`, "invalid_config_version"},
		{"string", `{"config_version":"2"}`, "invalid_config_version"},
		{"float", `{"config_version":2.1}`, "invalid_config_version"},
		{"boolean", `{"config_version":true}`, "invalid_config_version"},
		{"array", `{"config_version":[]}`, "invalid_config_version"},
		{"object", `{"config_version":{}}`, "invalid_config_version"},
		{"unsupported one", `{"config_version":1}`, "unsupported_config_version"},
		{"unsupported three", `{"config_version":3}`, "unsupported_config_version"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfigDocument([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestParseConfigDocumentRejectsInvalidConfigRevision(t *testing.T) {
	cases := []string{
		`0`,
		`-1`,
		`1.2`,
		`"1"`,
		`true`,
		`[]`,
		`{}`,
		`18446744073709551616`,
	}

	for _, revision := range cases {
		t.Run(revision, func(t *testing.T) {
			body := []byte(`{"config_version":2,"config_revision":` + revision + `}`)
			_, err := parseConfigDocument(body)
			if err == nil || !strings.Contains(err.Error(), "invalid_config_revision") {
				t.Fatalf("error = %v, want invalid_config_revision", err)
			}
		})
	}
}

func TestSaveV1DoesNotWriteConfigV2Metadata(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	if err := Save(&Config{SharedKey: NewSharedKey(), MaxHistory: 75}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "clipfan", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("config_version")) || bytes.Contains(data, []byte("config_revision")) {
		t.Fatalf("v1 save wrote v2 metadata: %s", data)
	}
}

func TestSaveRejectsConstructedConfigV2WhenGeneratedGateFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	version := 2
	revision := uint64(1)

	err := Save(&Config{
		ConfigVersion:  &version,
		ConfigRevision: &revision,
		SharedKey:      NewSharedKey(),
		MaxHistory:     75,
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("Save error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestSaveRejectsConstructedConfigRevisionWithoutVersionWhenGeneratedGateFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	revision := uint64(1)

	err := Save(&Config{
		ConfigRevision: &revision,
		SharedKey:      NewSharedKey(),
		MaxHistory:     75,
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("Save error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestSaveRejectsUnsupportedConstructedConfigVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	version := 3

	err := Save(&Config{
		ConfigVersion: &version,
		SharedKey:     NewSharedKey(),
		MaxHistory:    75,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported_config_version") {
		t.Fatalf("Save error = %v, want unsupported_config_version", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestUpdateConfigV2ScopedPreservesUnknownFieldsAndIncrementsRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"max_history": 50,
		"future_top": {"nested": [1, true, {"x": "y"}]},
		"ssh": {"peers": [{"id": "p1", "future": {"keep": true}}]}
	}`)
	before := readJSONMap(t, path)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONNumber(t, after["max_history"], 300)
	assertJSONValueEqual(t, before["future_top"], after["future_top"])
	assertJSONValueEqual(t, before["ssh"], after["ssh"])
}

func TestUpdateConfigV2ScopedFirstWritesStoreRevisionOne(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected RevisionExpectation
	}{
		{
			name: "pre v2",
			body: `{"shared_key":"k","max_history":50}`,
			expected: RevisionExpectation{
				State: RevisionStatePreV2,
			},
		},
		{
			name: "missing revision",
			body: `{"config_version":2,"shared_key":"k","max_history":50}`,
			expected: RevisionExpectation{
				State: RevisionStateMissingRevision,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, tc.body)
			err := updateConfigV2ScopedWithGate(path, true, tc.expected, func(c *Config) error {
				c.MaxHistory = 125
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			after := readJSONMap(t, path)
			assertJSONNumber(t, after["config_version"], 2)
			assertJSONNumber(t, after["config_revision"], 1)
			assertJSONNumber(t, after["max_history"], 125)
		})
	}
}

func TestUpdateConfigV2ScopedRejectsStaleRevisionWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(6),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("stale update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsRevisionOverflowWithoutWriting(t *testing.T) {
	const maxUint64 = ^uint64(0)
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":18446744073709551615,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(maxUint64),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_config_revision") {
		t.Fatalf("error = %v, want invalid_config_revision", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("overflow update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsInvalidExpectations(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"shared_key":"k","max_history":50}`)
	cases := []RevisionExpectation{
		{State: RevisionStateMissingRevision, Revision: uint64Ptr(0)},
		{State: RevisionStateVersioned},
		{State: RevisionStateVersioned, Revision: uint64Ptr(0)},
		{State: RevisionStatePreV2},
	}

	for _, expected := range cases {
		err := updateConfigV2ScopedWithGate(path, true, expected, func(c *Config) error {
			c.MaxHistory = 300
			return nil
		})
		if !errors.Is(err, ErrConfigRevisionConflict) {
			t.Fatalf("expectation %#v error = %v, want ErrConfigRevisionConflict", expected, err)
		}
	}
}

func TestUpdateConfigV2ScopedDisabledGeneratedGateFailsClosed(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = UpdateConfigV2Scoped(path, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedPrivateModeAndOwnTempCleanup(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":1,"shared_key":"k","max_history":50}`)
	dir := filepath.Dir(path)
	ownTemp := filepath.Join(dir, ".config-v2-config.json.tmp.123.1")
	unrelatedTemp := filepath.Join(dir, "config.json.tmp")
	otherWriterTemp := filepath.Join(dir, ".config-v2-other.json.tmp.123.1")
	for _, p := range []string{ownTemp, unrelatedTemp, otherWriterTemp} {
		if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(1),
	}, func(c *Config) error {
		c.MaxHistory = 500
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
	assertMode(t, path+".lock", 0o600)
	if _, err := os.Stat(ownTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own stale temp stat = %v, want removed", err)
	}
	for _, p := range []string{unrelatedTemp, otherWriterTemp} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unrelated temp %s stat = %v, want kept", p, err)
		}
	}
}

func TestUpdateConfigV2ScopedRejectsSymlinkedConfigFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	before := []byte(`{"config_version":2,"config_revision":1,"shared_key":"k","max_history":50}`)
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(1),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config path mode = %v, want symlink unchanged", info.Mode())
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("symlink target changed\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsMissingConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipfan", "config.json")
	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{State: RevisionStatePreV2}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want not exist", err)
	}
}

func writeConfigForV2Test(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertJSONNumber(t *testing.T, value any, want int64) {
	t.Helper()
	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value = %#v, want json.Number", value)
	}
	got, err := num.Int64()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("number = %d, want %d", got, want)
	}
}

func assertJSONValueEqual(t *testing.T, want, got any) {
	t.Helper()
	wantData, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wantData, gotData) {
		t.Fatalf("json value mismatch\nwant: %s\ngot:  %s", wantData, gotData)
	}
}

func sameRevision(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func revisionString(v *uint64) any {
	if v == nil {
		return nil
	}
	return *v
}

func uint64Ptr(n uint64) *uint64 {
	return &n
}
