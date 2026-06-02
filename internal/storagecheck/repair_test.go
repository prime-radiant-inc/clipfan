package storagecheck

import (
	"errors"
	"strings"
	"testing"
)

func TestRepairPromptForUnsupportedStorageRequiresNoDaemonEndpoint(t *testing.T) {
	results := []Result{{
		Role:           RootConfig,
		NormalizedPath: "/Users/me/Dropbox/clipfan",
		Code:           CodeUnsupportedRuntimeStorage,
		StorageClass:   ClassCloudSync,
		Reason:         "cloud_sync_root",
	}}

	prompt, ok := RepairPromptForResults(results, ErrUnsupportedRuntimeStorage)
	if !ok {
		t.Fatal("RepairPromptForResults did not return a prompt")
	}
	if prompt.Code != CodeUnsupportedRuntimeStorage {
		t.Fatalf("Code = %q, want %q", prompt.Code, CodeUnsupportedRuntimeStorage)
	}
	if prompt.SSHTransportEnabled {
		t.Fatal("repair prompt should report SSH transport disabled")
	}
	if prompt.RequiresDaemonEndpoint {
		t.Fatal("offline storage repair should not require a daemon endpoint")
	}
	text := prompt.Text()
	for _, want := range []string{"unsupported_runtime_storage", "daemon_endpoint_required: false", "Move clipfan config and daemon state", "/Users/me/Dropbox/clipfan"} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompt text missing %q:\n%s", want, text)
		}
	}
}

func TestRepairPromptForInconclusiveStorageUsesStableCode(t *testing.T) {
	prompt, ok := RepairPromptForResults(nil, errors.New("wrap: "+ErrStorageCheckInconclusive.Error()))
	if ok {
		t.Fatalf("plain text wrapping should not match sentinel errors: %#v", prompt)
	}

	prompt, ok = RepairPromptForResults(nil, ErrStorageCheckInconclusive)
	if !ok {
		t.Fatal("RepairPromptForResults did not return a prompt")
	}
	if prompt.Code != CodeStorageCheckInconclusive {
		t.Fatalf("Code = %q, want %q", prompt.Code, CodeStorageCheckInconclusive)
	}
	if !strings.Contains(prompt.Text(), "storage_check_inconclusive") {
		t.Fatalf("prompt text missing stable code:\n%s", prompt.Text())
	}
}
