package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
)

// DefaultMaxHistory bounds history when no explicit cap is given.
const DefaultMaxHistory = 200

const previewLen = 140

var urlRe = regexp.MustCompile(`^\s*https?://\S+\s*$`)

// historyMu serializes read-modify-write of history.json across goroutines.
var historyMu sync.Mutex

// HistoryEntry is one item in the clipboard history.
type HistoryEntry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // "text" | "image" | "link"
	Preview   string    `json:"preview"`
	Text      string    `json:"text,omitempty"`
	ImagePath string    `json:"image_path,omitempty"`
	SizeBytes int       `json:"size_bytes"`
	Origin    string    `json:"origin"`
	TS        time.Time `json:"ts"`
	Pinned    bool      `json:"pinned"`
}

func historyPath() string { return filepath.Join(config.StateDir(), "history.json") }

func classify(kind clipboard.Kind, body []byte) string {
	if kind == clipboard.KindImage {
		return "image"
	}
	if urlRe.Match(body) {
		return "link"
	}
	return "text"
}

func preview(body []byte, imagePath string) string {
	if imagePath != "" {
		return filepath.Base(imagePath)
	}
	s := strings.TrimSpace(string(body))
	if len(s) > previewLen {
		return s[:previewLen]
	}
	return s
}

func readHistory() ([]HistoryEntry, error) {
	data, err := os.ReadFile(historyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []HistoryEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func writeHistory(list []HistoryEntry) error {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	return writeAtomic(historyPath(), data, 0o644)
}

// AppendHistory records a clip. imagePath is the on-disk PNG path for images
// (empty for text). Re-copying identical content floats the existing entry to
// the top instead of duplicating. The list is trimmed to the cap (pinned exempt).
func AppendHistory(c clipboard.Content, origin, imagePath string) error {
	historyMu.Lock()
	defer historyMu.Unlock()

	list, err := readHistory()
	if err != nil {
		return err
	}
	id := hex.EncodeToString(c.Hash[:])
	entry := HistoryEntry{
		ID:        id,
		Kind:      classify(c.Kind, c.Bytes),
		Preview:   preview(c.Bytes, imagePath),
		SizeBytes: len(c.Bytes),
		Origin:    origin,
		TS:        c.TS,
	}
	if c.Kind == clipboard.KindImage {
		entry.ImagePath = imagePath
	} else {
		entry.Text = string(c.Bytes)
	}

	out := make([]HistoryEntry, 0, len(list)+1)
	for _, e := range list {
		if e.ID == id {
			entry.Pinned = e.Pinned // preserve pin across re-copy
			continue
		}
		out = append(out, e)
	}
	out = append([]HistoryEntry{entry}, out...) // newest first
	return writeHistory(capTrim(out, capLimit()))
}

// LoadHistory returns up to limit entries, pinned floated to the top, then
// newest-first within each group.
func LoadHistory(limit int) ([]HistoryEntry, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].TS.After(list[j].TS)
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// capLimit returns the configured history cap, or DefaultMaxHistory.
func capLimit() int {
	c, err := config.Load()
	if err == nil && c.MaxHistory > 0 {
		return c.MaxHistory
	}
	return DefaultMaxHistory
}

// SetPinned sets the pinned flag on the entry with the given id.
func SetPinned(id string, pinned bool) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == id {
			list[i].Pinned = pinned
		}
	}
	return writeHistory(list)
}

// DeleteEntry removes the entry with the given id.
func DeleteEntry(id string) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	out := make([]HistoryEntry, 0, len(list))
	for _, e := range list {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return writeHistory(out)
}

// ClearUnpinned removes every entry that is not pinned.
func ClearUnpinned() error {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return err
	}
	out := make([]HistoryEntry, 0, len(list))
	for _, e := range list {
		if e.Pinned {
			out = append(out, e)
		}
	}
	return writeHistory(out)
}

// EntryByID returns the entry with the given id, or ok=false if not found.
func EntryByID(id string) (HistoryEntry, bool, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return HistoryEntry{}, false, err
	}
	for _, e := range list {
		if e.ID == id {
			return e, true, nil
		}
	}
	return HistoryEntry{}, false, nil
}

// ReferencedImages returns the set of image filenames (<sha>.png) still
// referenced by any history entry, so image GC can avoid deleting them.
func ReferencedImages() (map[string]struct{}, error) {
	historyMu.Lock()
	defer historyMu.Unlock()
	list, err := readHistory()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(list))
	for _, e := range list {
		if e.ImagePath != "" {
			set[filepath.Base(e.ImagePath)] = struct{}{}
		}
	}
	return set, nil
}

// capTrim keeps all pinned entries plus the newest unpinned up to max total.
func capTrim(list []HistoryEntry, max int) []HistoryEntry {
	if len(list) <= max {
		return list
	}
	pinnedCount := 0
	for _, e := range list {
		if e.Pinned {
			pinnedCount++
		}
	}
	unpinnedBudget := max - pinnedCount
	if unpinnedBudget < 0 {
		unpinnedBudget = 0
	}
	kept := make([]HistoryEntry, 0, max)
	usedUnpinned := 0
	for _, e := range list { // newest-first
		if e.Pinned {
			kept = append(kept, e)
			continue
		}
		if usedUnpinned < unpinnedBudget {
			kept = append(kept, e)
			usedUnpinned++
		}
	}
	return kept
}
