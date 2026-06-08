package releaseflags

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type TransportGates struct {
	RemoteSecretWriteReleaseEnabled bool `json:"RemoteSecretWriteReleaseEnabled"`
	SSHPublicAddPeerSuccessEnabled  bool `json:"ssh_public_add_peer_success_enabled"`
}

type RuntimeGates struct {
	SSHReceivePrimitiveEnabled  bool `json:"ssh_receive_primitive_enabled"`
	SSHSyncStreamEnabled        bool `json:"ssh_sync_stream_enabled"`
	SSHPersistentCurrentEnabled bool `json:"ssh_persistent_current_enabled"`
	SSHSyncKeyRotationEnabled   bool `json:"ssh_sync_key_rotation_enabled"`
}

func ReadTransportGates(r io.Reader) (TransportGates, error) {
	var gates TransportGates
	err := readExactJSON(r, []string{
		"RemoteSecretWriteReleaseEnabled",
		"ssh_public_add_peer_success_enabled",
	}, &gates)
	return gates, err
}

func ReadRuntimeGates(r io.Reader) (RuntimeGates, error) {
	var gates RuntimeGates
	err := readExactJSON(r, []string{
		"ssh_receive_primitive_enabled",
		"ssh_sync_stream_enabled",
		"ssh_persistent_current_enabled",
		"ssh_sync_key_rotation_enabled",
	}, &gates)
	return gates, err
}

func ValidateGateBundle(transport TransportGates, runtime RuntimeGates) error {
	if transport.RemoteSecretWriteReleaseEnabled != transport.SSHPublicAddPeerSuccessEnabled {
		return fmt.Errorf("gate_order: RemoteSecretWriteReleaseEnabled and ssh_public_add_peer_success_enabled must match")
	}
	if !transport.RemoteSecretWriteReleaseEnabled && !transport.SSHPublicAddPeerSuccessEnabled {
		return nil
	}
	if !runtime.SSHReceivePrimitiveEnabled {
		return fmt.Errorf("missing_runtime_gate: ssh_receive_primitive_enabled")
	}
	if !runtime.SSHSyncStreamEnabled {
		return fmt.Errorf("missing_runtime_gate: ssh_sync_stream_enabled")
	}
	if !runtime.SSHPersistentCurrentEnabled {
		return fmt.Errorf("missing_runtime_gate: ssh_persistent_current_enabled")
	}
	if !runtime.SSHSyncKeyRotationEnabled {
		return fmt.Errorf("missing_runtime_gate: ssh_sync_key_rotation_enabled")
	}
	return nil
}

func readExactJSON(r io.Reader, expectedKeys []string, out any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	expected := make(map[string]struct{}, len(expectedKeys))
	for _, key := range expectedKeys {
		expected[key] = struct{}{}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	}
	if token != json.Delim('{') {
		return fmt.Errorf("malformed_manifest: expected object")
	}

	seen := make(map[string]struct{}, len(expectedKeys))
	var problems []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("malformed_manifest: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("malformed_manifest: expected object key")
		}

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fmt.Errorf("malformed_manifest: %w", err)
		}

		if _, ok := seen[key]; ok {
			problems = append(problems, fmt.Sprintf("duplicate_gate:%s", key))
			continue
		}
		seen[key] = struct{}{}

		if _, ok := expected[key]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected_gate:%s", key))
			continue
		}
		raw = bytes.TrimSpace(raw)
		if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
			problems = append(problems, fmt.Sprintf("gate_not_boolean:%s", key))
		}
	}

	if token, err := decoder.Token(); err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	} else if token != json.Delim('}') {
		return fmt.Errorf("malformed_manifest: expected object close")
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("malformed_manifest: %w", err)
		}
		return fmt.Errorf("malformed_manifest: trailing_data")
	}

	for _, key := range expectedKeys {
		if _, ok := seen[key]; !ok {
			problems = append(problems, fmt.Sprintf("missing_gate:%s", key))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid_gate_manifest: %s", strings.Join(problems, ", "))
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	}
	return nil
}
