package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateHostID(t *testing.T) {
	valid := []string{"m4", "Host_1.2", "a-b", strings.Repeat("a", 63)}
	for _, id := range valid {
		if err := ValidateHostID(id); err != nil {
			t.Fatalf("ValidateHostID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{"", "-host", "has space", "host/1", "host:1", strings.Repeat("a", 64)}
	for _, id := range invalid {
		if err := ValidateHostID(id); !errors.Is(err, ErrInvalidHostID) {
			t.Fatalf("ValidateHostID(%q) = %v, want ErrInvalidHostID", id, err)
		}
	}
}

func TestEnsurePersistedHostIDPreV2WritesLegacyHostnameOnly(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"shared_key": "k",
		"max_history": 50,
		"future": {"keep": true}
	}`)

	calls := 0
	result, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
		calls++
		return "Host_1.2", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("derive calls = %d, want 1", calls)
	}
	if !result.Migrated || result.HostID != "Host_1.2" || result.RevisionState != RevisionStatePreV2 || result.ConfigRevision != nil {
		t.Fatalf("result = %#v, want migrated pre-v2 Host_1.2", result)
	}

	after := readJSONMap(t, path)
	assertJSONValueEqual(t, "Host_1.2", after["hostname"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future"])
	if _, ok := after["config_version"]; ok {
		t.Fatalf("pre-v2 host migration wrote config_version: %#v", after["config_version"])
	}
	if _, ok := after["config_revision"]; ok {
		t.Fatalf("pre-v2 host migration wrote config_revision: %#v", after["config_revision"])
	}
	assertMode(t, path, 0o600)
	assertMode(t, path+".lock", 0o600)
}

func TestEnsurePersistedHostIDPreV2DropsStrayRevisionMetadata(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_revision": 7,
		"shared_key": "k",
		"max_history": 50
	}`)

	_, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
		return "m4", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONValueEqual(t, "m4", after["hostname"])
	if _, ok := after["config_version"]; ok {
		t.Fatalf("legacy host migration kept config_version: %#v", after["config_version"])
	}
	if _, ok := after["config_revision"]; ok {
		t.Fatalf("legacy host migration kept config_revision: %#v", after["config_revision"])
	}
}

func TestEnsurePersistedHostIDDoesNotRedriveExistingHostname(t *testing.T) {
	path := writeConfigForV2Test(t, `{"shared_key":"k","hostname":"locked-host","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
		return "", errors.New("derive should not be called")
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Migrated || result.HostID != "locked-host" {
		t.Fatalf("result = %#v, want existing locked-host without migration", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("existing host ID migration changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestEnsurePersistedHostIDRejectsInvalidExistingOrDerivedHostID(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"shared_key":"k","hostname":"bad host","max_history":50}`)
		if _, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
			return "unused", nil
		}); !errors.Is(err, ErrInvalidHostID) {
			t.Fatalf("error = %v, want ErrInvalidHostID", err)
		}
	})

	t.Run("derived", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"shared_key":"k","max_history":50}`)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
			return "-bad", nil
		}); !errors.Is(err, ErrInvalidHostID) {
			t.Fatalf("error = %v, want ErrInvalidHostID", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("invalid derived host ID changed file\nbefore: %s\nafter: %s", before, after)
		}
	})
}

func TestEnsurePersistedHostIDV2PreservesUnknownAndIncrementsRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"max_history": 50,
		"ssh": {"peers": [{"id": "p1", "future": {"keep": true}}]}
	}`)
	before := readJSONMap(t, path)

	result, err := ensurePersistedHostIDWithGate(path, true, func() (string, error) {
		return "m4", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Migrated || result.HostID != "m4" || result.RevisionState != RevisionStateVersioned || result.ConfigRevision == nil || *result.ConfigRevision != 8 {
		t.Fatalf("result = %#v, want migrated v2 revision 8", result)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, "m4", after["hostname"])
	assertJSONValueEqual(t, before["ssh"], after["ssh"])
}

func TestEnsurePersistedHostIDV2MissingRevisionStartsRevisionOne(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"shared_key":"k","max_history":50}`)

	result, err := ensurePersistedHostIDWithGate(path, true, func() (string, error) {
		return "m4", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigRevision == nil || *result.ConfigRevision != 1 {
		t.Fatalf("ConfigRevision = %v, want 1", revisionString(result.ConfigRevision))
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 1)
}

func TestEnsurePersistedHostIDV2GateFalseFailsClosedWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ensurePersistedHostIDWithGate(path, false, func() (string, error) {
		return "m4", nil
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled v2 host migration changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestRequirePersistedHostID(t *testing.T) {
	if _, err := RequirePersistedHostID(nil); !errors.Is(err, ErrHostIDNotPersisted) {
		t.Fatalf("nil config error = %v, want ErrHostIDNotPersisted", err)
	}
	if _, err := RequirePersistedHostID(&Config{}); !errors.Is(err, ErrHostIDNotPersisted) {
		t.Fatalf("missing host error = %v, want ErrHostIDNotPersisted", err)
	}
	if _, err := RequirePersistedHostID(&Config{Hostname: "bad host"}); !errors.Is(err, ErrInvalidHostID) {
		t.Fatalf("invalid host error = %v, want ErrInvalidHostID", err)
	}
	hostID, err := RequirePersistedHostID(&Config{Hostname: "m4"})
	if err != nil {
		t.Fatal(err)
	}
	if hostID != "m4" {
		t.Fatalf("hostID = %q, want m4", hostID)
	}
}

func TestRequirePersistedHostIDAtRevision(t *testing.T) {
	t.Run("pre v2", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"shared_key":"k","hostname":"m4","max_history":50}`)
		result, err := RequirePersistedHostIDAtRevision(path, "m4", RevisionExpectation{State: RevisionStatePreV2})
		if err != nil {
			t.Fatal(err)
		}
		if result.HostID != "m4" || result.RevisionState != RevisionStatePreV2 || result.ConfigRevision != nil {
			t.Fatalf("result = %#v, want pre-v2 m4", result)
		}
	})

	t.Run("v2 revisioned", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","max_history":50}`)
		result, err := RequirePersistedHostIDAtRevision(path, "m4", RevisionExpectation{
			State:    RevisionStateVersioned,
			Revision: uint64Ptr(7),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.ConfigRevision == nil || *result.ConfigRevision != 7 {
			t.Fatalf("ConfigRevision = %v, want 7", revisionString(result.ConfigRevision))
		}
	})

	t.Run("missing", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"shared_key":"k","max_history":50}`)
		_, err := RequirePersistedHostIDAtRevision(path, "m4", RevisionExpectation{State: RevisionStatePreV2})
		if !errors.Is(err, ErrHostIDNotPersisted) {
			t.Fatalf("error = %v, want ErrHostIDNotPersisted", err)
		}
	})

	t.Run("host mismatch", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"shared_key":"k","hostname":"m4","max_history":50}`)
		_, err := RequirePersistedHostIDAtRevision(path, "other", RevisionExpectation{State: RevisionStatePreV2})
		if !errors.Is(err, ErrHostIDMismatch) {
			t.Fatalf("error = %v, want ErrHostIDMismatch", err)
		}
	})

	t.Run("stale revision", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","max_history":50}`)
		_, err := RequirePersistedHostIDAtRevision(path, "m4", RevisionExpectation{
			State:    RevisionStateVersioned,
			Revision: uint64Ptr(6),
		})
		if !errors.Is(err, ErrConfigRevisionConflict) {
			t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
		}
	})
}

func TestEnsurePersistedHostIDRejectsSymlinkedConfigFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"shared_key":"k","max_history":50}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	_, err := ensurePersistedHostIDWithGate(path, false, func() (string, error) {
		return "m4", nil
	})
	if err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}
}
