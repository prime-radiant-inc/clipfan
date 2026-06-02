package releaseflags

import (
	"strings"
	"testing"
)

func TestReadTransportGatesMapsBooleanValues(t *testing.T) {
	gates, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": true,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": true,
		"ssh_public_add_peer_success_enabled": false
	}`))
	if err != nil {
		t.Fatal(err)
	}

	if !gates.PeerHTTPRuntimeDisabled {
		t.Fatal("PeerHTTPRuntimeDisabled = false, want true")
	}
	if gates.ConfigV2WriteEnabled {
		t.Fatal("ConfigV2WriteEnabled = true, want false")
	}
	if !gates.RemoteSecretWriteReleaseEnabled {
		t.Fatal("RemoteSecretWriteReleaseEnabled = false, want true")
	}
	if gates.SSHPublicAddPeerSuccessEnabled {
		t.Fatal("SSHPublicAddPeerSuccessEnabled = true, want false")
	}
}

func TestReadRuntimeGatesMapsBooleanValues(t *testing.T) {
	gates, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": true,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": true,
		"ssh_sync_key_rotation_enabled": false
	}`))
	if err != nil {
		t.Fatal(err)
	}

	if !gates.SSHReceivePrimitiveEnabled {
		t.Fatal("SSHReceivePrimitiveEnabled = false, want true")
	}
	if gates.SSHSyncStreamEnabled {
		t.Fatal("SSHSyncStreamEnabled = true, want false")
	}
	if !gates.SSHPersistentCurrentEnabled {
		t.Fatal("SSHPersistentCurrentEnabled = false, want true")
	}
	if gates.SSHSyncKeyRotationEnabled {
		t.Fatal("SSHSyncKeyRotationEnabled = true, want false")
	}
}

func TestReadTransportGatesRejectsMissingAndUnexpectedKeys(t *testing.T) {
	_, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": false,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": false,
		"unexpected": false
	}`))
	assertErrorContains(t, err, "missing_gate")
	assertErrorContains(t, err, "unexpected_gate")
}

func TestReadRuntimeGatesRejectsMissingKeys(t *testing.T) {
	_, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": false,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": false
	}`))
	assertErrorContains(t, err, "missing_gate")
}

func TestReadTransportGatesRejectsNullAndNonBooleanValues(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "null",
			json: `{
				"PeerHTTPRuntimeDisabled": null,
				"ConfigV2WriteEnabled": false,
				"RemoteSecretWriteReleaseEnabled": false,
				"ssh_public_add_peer_success_enabled": false
			}`,
		},
		{
			name: "string",
			json: `{
				"PeerHTTPRuntimeDisabled": "false",
				"ConfigV2WriteEnabled": false,
				"RemoteSecretWriteReleaseEnabled": false,
				"ssh_public_add_peer_success_enabled": false
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadTransportGates(strings.NewReader(tc.json))
			assertErrorContains(t, err, "gate_not_boolean")
		})
	}
}

func TestReadRuntimeGatesRejectsDuplicateKeys(t *testing.T) {
	_, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": false,
		"ssh_receive_primitive_enabled": true,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": false,
		"ssh_sync_key_rotation_enabled": false
	}`))
	assertErrorContains(t, err, "duplicate_gate")
}

func TestReadTransportGatesRejectsMalformedJSON(t *testing.T) {
	_, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": false,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": false,
		"ssh_public_add_peer_success_enabled": false,
	`))
	assertErrorContains(t, err, "malformed_manifest")
}

func TestReadRuntimeGatesRejectsTrailingData(t *testing.T) {
	_, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": false,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": false,
		"ssh_sync_key_rotation_enabled": false
	} false`))
	assertErrorContains(t, err, "malformed_manifest")
}

func TestValidateGateBundleAcceptsAllFalseBundle(t *testing.T) {
	if err := ValidateGateBundle(TransportGates{}, RuntimeGates{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGateBundleAccepts17d3aLocalCutoverBundle(t *testing.T) {
	transport := TransportGates{
		PeerHTTPRuntimeDisabled:         true,
		ConfigV2WriteEnabled:            true,
		RemoteSecretWriteReleaseEnabled: false,
		SSHPublicAddPeerSuccessEnabled:  false,
	}
	if err := ValidateGateBundle(transport, RuntimeGates{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGateBundleAccepts17d3bPublicAddPeerBundle(t *testing.T) {
	transport := TransportGates{
		PeerHTTPRuntimeDisabled:         true,
		ConfigV2WriteEnabled:            true,
		RemoteSecretWriteReleaseEnabled: true,
		SSHPublicAddPeerSuccessEnabled:  true,
	}
	runtime := RuntimeGates{
		SSHReceivePrimitiveEnabled:  true,
		SSHSyncStreamEnabled:        true,
		SSHPersistentCurrentEnabled: true,
		SSHSyncKeyRotationEnabled:   true,
	}
	if err := ValidateGateBundle(transport, runtime); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGateBundleRejectsInvalidPublicOrdering(t *testing.T) {
	transport := TransportGates{
		PeerHTTPRuntimeDisabled: true,
		ConfigV2WriteEnabled:    false,
	}
	err := ValidateGateBundle(transport, RuntimeGates{})
	assertErrorContains(t, err, "gate_order")
}

func TestValidateGateBundleRejectsRemoteSecretAndPublicAddPeerMismatch(t *testing.T) {
	transport := TransportGates{
		PeerHTTPRuntimeDisabled:         true,
		ConfigV2WriteEnabled:            true,
		RemoteSecretWriteReleaseEnabled: true,
		SSHPublicAddPeerSuccessEnabled:  false,
	}
	err := ValidateGateBundle(transport, RuntimeGates{})
	assertErrorContains(t, err, "gate_order")
}

func TestValidateGateBundleRequiresRuntimeGatesForPublicAddPeerAndSecrets(t *testing.T) {
	transport := TransportGates{
		PeerHTTPRuntimeDisabled:         true,
		ConfigV2WriteEnabled:            true,
		RemoteSecretWriteReleaseEnabled: true,
		SSHPublicAddPeerSuccessEnabled:  true,
	}
	runtime := RuntimeGates{
		SSHReceivePrimitiveEnabled:  true,
		SSHSyncStreamEnabled:        true,
		SSHPersistentCurrentEnabled: true,
		SSHSyncKeyRotationEnabled:   false,
	}
	err := ValidateGateBundle(transport, runtime)
	assertErrorContains(t, err, "missing_runtime_gate")
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want substring %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}
