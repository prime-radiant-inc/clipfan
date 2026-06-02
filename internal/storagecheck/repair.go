package storagecheck

import (
	"errors"
	"fmt"
	"strings"
)

type RepairPrompt struct {
	Code                   Code
	Title                  string
	Message                string
	Results                []Result
	SSHTransportEnabled    bool
	RequiresDaemonEndpoint bool
}

func RepairPromptForResults(results []Result, err error) (RepairPrompt, bool) {
	switch {
	case errors.Is(err, ErrUnsupportedRuntimeStorage):
		return RepairPrompt{
			Code:                   CodeUnsupportedRuntimeStorage,
			Title:                  "Unsupported clipfan runtime storage",
			Message:                "Move clipfan config and daemon state to local per-host storage, then restart clipfan. Network homes, shared homes, and cloud-synced folders are not supported for SSH transport.",
			Results:                results,
			SSHTransportEnabled:    false,
			RequiresDaemonEndpoint: false,
		}, true
	case errors.Is(err, ErrStorageCheckInconclusive):
		return RepairPrompt{
			Code:                   CodeStorageCheckInconclusive,
			Title:                  "Clipfan could not verify runtime storage",
			Message:                "Move clipfan config and daemon state to local per-host storage or fix the storage permissions, then restart clipfan. SSH transport stays disabled until the local storage check passes.",
			Results:                results,
			SSHTransportEnabled:    false,
			RequiresDaemonEndpoint: false,
		}, true
	default:
		return RepairPrompt{}, false
	}
}

func (p RepairPrompt) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", p.Title)
	fmt.Fprintf(&b, "code: %s\n", p.Code)
	fmt.Fprintf(&b, "ssh_transport_enabled: %t\n", p.SSHTransportEnabled)
	fmt.Fprintf(&b, "daemon_endpoint_required: %t\n", p.RequiresDaemonEndpoint)
	fmt.Fprintf(&b, "%s\n", p.Message)
	for _, result := range p.Results {
		fmt.Fprintf(&b, "- %s: %s", result.Role, result.NormalizedPath)
		if result.StorageClass != "" {
			fmt.Fprintf(&b, " (%s)", result.StorageClass)
		}
		if result.Reason != "" {
			fmt.Fprintf(&b, " reason=%s", result.Reason)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}
