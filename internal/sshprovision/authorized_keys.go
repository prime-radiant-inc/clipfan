package sshprovision

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

const (
	authorizedKeyMarkerPrefix = "clipfan-sync:"
	managedKeyType            = "ssh-ed25519"
)

var (
	ErrInvalidAuthorizedKey  = errors.New("invalid_authorized_key")
	ErrAuthorizedKeyConflict = errors.New("authorized_key_conflict")
)

var authorizedKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

type ManagedAuthorizedKey struct {
	PeerID      string
	KeyID       string
	GatewayPath string
	PublicKey   string
}

type ManagedAuthorizedKeyMetadata struct {
	PeerID string
	KeyID  string
}

func NewManagedAuthorizedKey(entry ManagedAuthorizedKey) (ManagedAuthorizedKey, error) {
	if err := config.ValidateHostID(entry.PeerID); err != nil {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid peer id: %v", ErrInvalidAuthorizedKey, err)
	}
	if !authorizedKeyIDPattern.MatchString(entry.KeyID) {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid key id", ErrInvalidAuthorizedKey)
	}
	if err := config.ValidateSSHExecutablePath(entry.GatewayPath); err != nil {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid gateway path: %v", ErrInvalidAuthorizedKey, err)
	}
	keyType, err := publicKeyType(entry.PublicKey)
	if err != nil {
		return ManagedAuthorizedKey{}, err
	}
	if keyType != managedKeyType {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: unsupported public key type %q", ErrInvalidAuthorizedKey, keyType)
	}
	return entry, nil
}

func (entry ManagedAuthorizedKey) ForcedCommand() string {
	return entry.GatewayPath + " ssh-gateway --authorized-peer " + entry.PeerID + " --authorized-key-id " + entry.KeyID
}

func (entry ManagedAuthorizedKey) Line() string {
	return `no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty,no-user-rc,command="` + escapeAuthorizedKeyOption(entry.ForcedCommand()) + `" ` +
		managedKeyType + " " + entry.PublicKey + " " + authorizedKeyMarkerPrefix + entry.PeerID + ":" + entry.KeyID
}

func UpsertManagedAuthorizedKeyLine(data []byte, entry ManagedAuthorizedKey) ([]byte, error) {
	if _, err := NewManagedAuthorizedKey(entry); err != nil {
		return nil, err
	}

	lines, trailingNewline := splitAuthorizedKeyLines(string(data))
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		metadata, managed, err := ParseManagedAuthorizedKeyMetadata(line)
		if err != nil {
			return nil, err
		}
		if !managed {
			out = append(out, line)
			continue
		}
		if metadata.PeerID == entry.PeerID {
			if !replaced {
				out = append(out, entry.Line())
				replaced = true
			}
			continue
		}
		if metadata.KeyID == entry.KeyID {
			return nil, fmt.Errorf("%w: key id %s already belongs to peer %s", ErrAuthorizedKeyConflict, entry.KeyID, metadata.PeerID)
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, entry.Line())
		trailingNewline = true
	}
	return joinAuthorizedKeyLines(out, trailingNewline), nil
}

func ParseManagedAuthorizedKeyMetadata(line string) (ManagedAuthorizedKeyMetadata, bool, error) {
	fields := strings.Fields(line)
	markerIndex := -1
	marker := ""
	for i, field := range fields {
		if strings.HasPrefix(field, authorizedKeyMarkerPrefix) {
			markerIndex = i
			marker = field
			break
		}
	}
	if markerIndex == -1 {
		return ManagedAuthorizedKeyMetadata{}, false, nil
	}

	metadata, err := parseManagedAuthorizedKeyMarker(marker)
	if err != nil {
		return ManagedAuthorizedKeyMetadata{}, true, err
	}
	if err := validateManagedAuthorizedKeyLine(line, fields, markerIndex, metadata); err != nil {
		return ManagedAuthorizedKeyMetadata{}, true, err
	}
	return metadata, true, nil
}

func parseManagedAuthorizedKeyMarker(marker string) (ManagedAuthorizedKeyMetadata, error) {
	value := strings.TrimPrefix(marker, authorizedKeyMarkerPrefix)
	peerID, keyID, ok := strings.Cut(value, ":")
	if !ok {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: malformed managed marker", ErrAuthorizedKeyConflict)
	}
	metadata := ManagedAuthorizedKeyMetadata{PeerID: peerID, KeyID: keyID}
	if err := config.ValidateHostID(metadata.PeerID); err != nil {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: invalid managed peer id", ErrAuthorizedKeyConflict)
	}
	if !authorizedKeyIDPattern.MatchString(metadata.KeyID) {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: invalid managed key id", ErrAuthorizedKeyConflict)
	}
	return metadata, nil
}

func validateManagedAuthorizedKeyLine(line string, fields []string, markerIndex int, metadata ManagedAuthorizedKeyMetadata) error {
	if markerIndex < 2 || fields[markerIndex-2] != managedKeyType {
		return fmt.Errorf("%w: malformed managed key line", ErrAuthorizedKeyConflict)
	}
	keyType, err := publicKeyType(fields[markerIndex-1])
	if err != nil || keyType != managedKeyType {
		return fmt.Errorf("%w: invalid managed public key", ErrAuthorizedKeyConflict)
	}
	command, ok := parseAuthorizedKeyCommandOption(line)
	if !ok {
		return fmt.Errorf("%w: missing managed forced command", ErrAuthorizedKeyConflict)
	}
	expectedSuffix := " ssh-gateway --authorized-peer " + metadata.PeerID + " --authorized-key-id " + metadata.KeyID
	if !strings.HasSuffix(command, expectedSuffix) {
		return fmt.Errorf("%w: managed forced command metadata mismatch", ErrAuthorizedKeyConflict)
	}
	gatewayPath := strings.TrimSuffix(command, expectedSuffix)
	if err := config.ValidateSSHExecutablePath(gatewayPath); err != nil {
		return fmt.Errorf("%w: invalid managed gateway path", ErrAuthorizedKeyConflict)
	}
	return nil
}

func parseAuthorizedKeyCommandOption(line string) (string, bool) {
	for i := 0; i < len(line); i++ {
		if !strings.HasPrefix(line[i:], `command="`) {
			continue
		}
		value, ok := readAuthorizedKeyQuotedOption(line[i+len(`command="`):])
		if ok {
			return value, true
		}
	}
	return "", false
}

func readAuthorizedKeyQuotedOption(value string) (string, bool) {
	var out strings.Builder
	escaped := false
	for _, ch := range value {
		if escaped {
			out.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return out.String(), true
		}
		out.WriteRune(ch)
	}
	return "", false
}

func publicKeyType(publicKey string) (string, error) {
	if strings.TrimSpace(publicKey) != publicKey || strings.ContainsAny(publicKey, "\x00\r\n\t ") || publicKey == "" {
		return "", fmt.Errorf("%w: invalid public key", ErrInvalidAuthorizedKey)
	}
	blob, err := decodeKnownHostPublicKeyBlob(publicKey)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidAuthorizedKey, err)
	}
	keyType, err := readSSHString(blob)
	if err != nil {
		return "", fmt.Errorf("%w: malformed public key blob", ErrInvalidAuthorizedKey)
	}
	return keyType, nil
}

func escapeAuthorizedKeyOption(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func splitAuthorizedKeyLines(data string) ([]string, bool) {
	if data == "" {
		return nil, true
	}
	trailingNewline := strings.HasSuffix(data, "\n")
	trimmed := strings.TrimSuffix(data, "\n")
	if trimmed == "" {
		return nil, trailingNewline
	}
	return strings.Split(trimmed, "\n"), trailingNewline
}

func joinAuthorizedKeyLines(lines []string, trailingNewline bool) []byte {
	if len(lines) == 0 {
		if trailingNewline {
			return []byte("\n")
		}
		return nil
	}
	out := strings.Join(lines, "\n")
	if trailingNewline {
		out += "\n"
	}
	return []byte(out)
}
