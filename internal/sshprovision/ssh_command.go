package sshprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

var ErrInvalidPinnedSSHCommand = errors.New("invalid_pinned_ssh_command")
var ErrInvalidRegularSSHCommand = errors.New("invalid_regular_ssh_command")

type PinnedSSHCommand struct {
	User           string
	Host           string
	Port           int
	PrivateKeyPath string
	KnownHostsPath string
}

type SSHCommand struct {
	Args []string
}

type SSHKeyscanSpec struct {
	Host           string
	Port           int
	TimeoutSeconds int
}

type RegularSSHInstallAuthorizedKeySpec struct {
	User           string
	Host           string
	Port           int
	KnownHostsPath string
	InstallPath    string
	GatewayPath    string
	PeerID         string
	KeyID          string
	PublicKey      string
}

type RegularSSHEnsureSyncKeySpec struct {
	User           string
	Host           string
	Port           int
	KnownHostsPath string
	InstallPath    string
	HostID         string
	KeyPath        string
}

type RegularSSHInstallKnownHostSpec struct {
	User                 string
	Host                 string
	Port                 int
	KnownHostsPath       string
	InstallPath          string
	TargetKnownHostsPath string
	TargetHost           string
	TargetPort           int
	KeyType              string
	PublicKey            string
}

func PinnedSSHProbeCommand(spec PinnedSSHCommand) (SSHCommand, error) {
	normalized, err := normalizePinnedSSHCommand(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	return SSHCommand{Args: []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + normalized.KnownHostsPath,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-i", normalized.PrivateKeyPath,
		"-p", strconv.Itoa(normalized.Port),
		normalized.User + "@" + normalized.Host,
		SSHGatewayProbeCommand,
	}}, nil
}

func SSHKeyscanCommand(spec SSHKeyscanSpec) (SSHCommand, error) {
	normalized, err := normalizeSSHKeyscanSpec(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	return SSHCommand{Args: []string{
		"ssh-keyscan",
		"-T", strconv.Itoa(normalized.TimeoutSeconds),
		"-p", strconv.Itoa(normalized.Port),
		normalized.Host,
	}}, nil
}

func RegularSSHInstallAuthorizedKeyCommand(spec RegularSSHInstallAuthorizedKeySpec) (SSHCommand, error) {
	normalized, err := normalizeRegularSSHInstallAuthorizedKeySpec(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	remoteCommand := shellQuoteCommand([]string{
		normalized.InstallPath,
		"ssh-install-authorized-key",
		"--peer", normalized.PeerID,
		"--key-id", normalized.KeyID,
		"--gateway-path", normalized.GatewayPath,
		"--public-key", normalized.PublicKey,
	})
	return SSHCommand{Args: []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + normalized.KnownHostsPath,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(normalized.Port),
		normalized.User + "@" + normalized.Host,
		remoteCommand,
	}}, nil
}

func RegularSSHEnsureSyncKeyCommand(spec RegularSSHEnsureSyncKeySpec) (SSHCommand, error) {
	normalized, err := normalizeRegularSSHEnsureSyncKeySpec(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	return regularSSHRemoteCommand(regularSSHRemoteSpec{
		User:           normalized.User,
		Host:           normalized.Host,
		Port:           normalized.Port,
		KnownHostsPath: normalized.KnownHostsPath,
		RemoteArgs: []string{
			normalized.InstallPath,
			"ssh-ensure-sync-key",
			"--host-id", normalized.HostID,
			"--key-path", normalized.KeyPath,
		},
	}), nil
}

func RegularSSHInstallKnownHostCommand(spec RegularSSHInstallKnownHostSpec) (SSHCommand, error) {
	normalized, pin, err := normalizeRegularSSHInstallKnownHostSpec(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	return regularSSHRemoteCommand(regularSSHRemoteSpec{
		User:           normalized.User,
		Host:           normalized.Host,
		Port:           normalized.Port,
		KnownHostsPath: normalized.KnownHostsPath,
		RemoteArgs: []string{
			normalized.InstallPath,
			"ssh-install-known-host",
			"--known-hosts", normalized.TargetKnownHostsPath,
			"--host", normalized.TargetHost,
			"--port", strconv.Itoa(normalized.TargetPort),
			"--key-type", pin.KeyType,
			"--public-key", pin.PublicKey,
		},
	}), nil
}

func SyncKeyMaterialFromConfig(result config.SyncKeyCreateResult) (SyncKeyMaterial, error) {
	fields := strings.Fields(result.PublicKey)
	if len(fields) < 2 || fields[0] != managedKeyType {
		return SyncKeyMaterial{}, fmt.Errorf("%w: invalid sync public key", ErrInvalidAuthorizedKey)
	}
	blob, err := decodeKnownHostPublicKeyBlob(fields[1])
	if err != nil {
		return SyncKeyMaterial{}, fmt.Errorf("%w: invalid sync public key: %v", ErrInvalidAuthorizedKey, err)
	}
	derivedKeyID := syncKeyIDFromPublicBlob(blob)
	if result.KeyID != "" && result.KeyID != derivedKeyID {
		return SyncKeyMaterial{}, fmt.Errorf("%w: sync key id mismatch", ErrInvalidAuthorizedKey)
	}
	material := SyncKeyMaterial{
		PrivateKeyPath: result.PrivateKeyPath,
		PublicKey:      fields[1],
		KeyID:          derivedKeyID,
	}
	if err := validateSyncKeyMaterial(material); err != nil {
		return SyncKeyMaterial{}, err
	}
	return material, nil
}

type regularSSHRemoteSpec struct {
	User           string
	Host           string
	Port           int
	KnownHostsPath string
	RemoteArgs     []string
}

func regularSSHRemoteCommand(spec regularSSHRemoteSpec) SSHCommand {
	return SSHCommand{Args: []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + spec.KnownHostsPath,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(spec.Port),
		spec.User + "@" + spec.Host,
		shellQuoteCommand(spec.RemoteArgs),
	}}
}

func normalizePinnedSSHCommand(spec PinnedSSHCommand) (PinnedSSHCommand, error) {
	if err := config.ValidateSSHUser(spec.User); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid user: %v", ErrInvalidPinnedSSHCommand, err)
	}
	host, err := config.CanonicalSSHHost(spec.Host)
	if err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid host: %v", ErrInvalidPinnedSSHCommand, err)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid port %d", ErrInvalidPinnedSSHCommand, spec.Port)
	}
	if err := config.ValidateSSHExecutablePath(spec.PrivateKeyPath); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid private key path: %v", ErrInvalidPinnedSSHCommand, err)
	}
	if err := config.ValidateSSHExecutablePath(spec.KnownHostsPath); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid known hosts path: %v", ErrInvalidPinnedSSHCommand, err)
	}
	spec.Host = host
	return spec, nil
}

func normalizeSSHKeyscanSpec(spec SSHKeyscanSpec) (SSHKeyscanSpec, error) {
	host, err := config.CanonicalSSHHost(spec.Host)
	if err != nil {
		return SSHKeyscanSpec{}, fmt.Errorf("%w: invalid host: %v", ErrInvalidRegularSSHCommand, err)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return SSHKeyscanSpec{}, fmt.Errorf("%w: invalid port %d", ErrInvalidRegularSSHCommand, spec.Port)
	}
	if spec.TimeoutSeconds == 0 {
		spec.TimeoutSeconds = 5
	}
	if spec.TimeoutSeconds < 1 || spec.TimeoutSeconds > 60 {
		return SSHKeyscanSpec{}, fmt.Errorf("%w: invalid timeout %d", ErrInvalidRegularSSHCommand, spec.TimeoutSeconds)
	}
	spec.Host = host
	return spec, nil
}

func normalizeRegularSSHInstallAuthorizedKeySpec(spec RegularSSHInstallAuthorizedKeySpec) (RegularSSHInstallAuthorizedKeySpec, error) {
	user, host, err := normalizeRegularSSHTarget(spec.User, spec.Host, spec.Port, spec.KnownHostsPath)
	if err != nil {
		return RegularSSHInstallAuthorizedKeySpec{}, err
	}
	if err := config.ValidateSSHExecutablePath(spec.InstallPath); err != nil {
		return RegularSSHInstallAuthorizedKeySpec{}, fmt.Errorf("%w: invalid install path: %v", ErrInvalidRegularSSHCommand, err)
	}
	if _, err := NewManagedAuthorizedKey(ManagedAuthorizedKey{
		PeerID:      spec.PeerID,
		KeyID:       spec.KeyID,
		GatewayPath: spec.GatewayPath,
		PublicKey:   spec.PublicKey,
	}); err != nil {
		return RegularSSHInstallAuthorizedKeySpec{}, fmt.Errorf("%w: invalid managed key: %v", ErrInvalidRegularSSHCommand, err)
	}
	spec.User = user
	spec.Host = host
	return spec, nil
}

func normalizeRegularSSHEnsureSyncKeySpec(spec RegularSSHEnsureSyncKeySpec) (RegularSSHEnsureSyncKeySpec, error) {
	user, host, err := normalizeRegularSSHTarget(spec.User, spec.Host, spec.Port, spec.KnownHostsPath)
	if err != nil {
		return RegularSSHEnsureSyncKeySpec{}, err
	}
	if err := config.ValidateSSHExecutablePath(spec.InstallPath); err != nil {
		return RegularSSHEnsureSyncKeySpec{}, fmt.Errorf("%w: invalid install path: %v", ErrInvalidRegularSSHCommand, err)
	}
	if err := config.ValidateHostID(spec.HostID); err != nil {
		return RegularSSHEnsureSyncKeySpec{}, fmt.Errorf("%w: invalid host id: %v", ErrInvalidRegularSSHCommand, err)
	}
	if err := config.ValidateSyncKeyPath(spec.KeyPath); err != nil {
		return RegularSSHEnsureSyncKeySpec{}, fmt.Errorf("%w: invalid key path: %v", ErrInvalidRegularSSHCommand, err)
	}
	spec.User = user
	spec.Host = host
	return spec, nil
}

func normalizeRegularSSHInstallKnownHostSpec(spec RegularSSHInstallKnownHostSpec) (RegularSSHInstallKnownHostSpec, KnownHostPin, error) {
	user, host, err := normalizeRegularSSHTarget(spec.User, spec.Host, spec.Port, spec.KnownHostsPath)
	if err != nil {
		return RegularSSHInstallKnownHostSpec{}, KnownHostPin{}, err
	}
	if err := config.ValidateSSHExecutablePath(spec.InstallPath); err != nil {
		return RegularSSHInstallKnownHostSpec{}, KnownHostPin{}, fmt.Errorf("%w: invalid install path: %v", ErrInvalidRegularSSHCommand, err)
	}
	if err := config.ValidateSSHExecutablePath(spec.TargetKnownHostsPath); err != nil {
		return RegularSSHInstallKnownHostSpec{}, KnownHostPin{}, fmt.Errorf("%w: invalid target known hosts path: %v", ErrInvalidRegularSSHCommand, err)
	}
	pin, err := NewKnownHostPin(spec.TargetHost, spec.TargetPort, spec.KeyType, spec.PublicKey)
	if err != nil {
		return RegularSSHInstallKnownHostSpec{}, KnownHostPin{}, fmt.Errorf("%w: invalid known host pin: %v", ErrInvalidRegularSSHCommand, err)
	}
	spec.User = user
	spec.Host = host
	spec.TargetHost = strings.TrimPrefix(strings.TrimSuffix(pin.Pattern, "]:"+strconv.Itoa(spec.TargetPort)), "[")
	return spec, pin, nil
}

func normalizeRegularSSHTarget(user string, host string, port int, knownHostsPath string) (string, string, error) {
	if err := config.ValidateSSHUser(user); err != nil {
		return "", "", fmt.Errorf("%w: invalid user: %v", ErrInvalidRegularSSHCommand, err)
	}
	canonicalHost, err := config.CanonicalSSHHost(host)
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid host: %v", ErrInvalidRegularSSHCommand, err)
	}
	if port < 1 || port > 65535 {
		return "", "", fmt.Errorf("%w: invalid port %d", ErrInvalidRegularSSHCommand, port)
	}
	if err := config.ValidateSSHExecutablePath(knownHostsPath); err != nil {
		return "", "", fmt.Errorf("%w: invalid known hosts path: %v", ErrInvalidRegularSSHCommand, err)
	}
	return user, canonicalHost, nil
}

func shellQuoteCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func syncKeyIDFromPublicBlob(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}
