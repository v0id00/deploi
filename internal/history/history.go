// Package history records and manages deploy history for rollback support.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry records a single deploy operation.
type Entry struct {
	ID         int       `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Operation  string    `json:"operation"` // push, pull, sync
	Profile    string    `json:"profile,omitempty"`
	Method     string    `json:"method"`
	Servers    []string  `json:"servers"`
	Files      int       `json:"files"`
	FileList   []string  `json:"file_list,omitempty"`
	Source     string    `json:"source,omitempty"` // git-diff, git-commit, manual, etc.
	Commit     string    `json:"commit,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	RemotePath string    `json:"remote_path"`
	Status     string    `json:"status"` // ok, partial, error
	Elapsed    string    `json:"elapsed"`
	Notes      string    `json:"notes,omitempty"`

	// For rollback: we store the backup path on the remote
	BackupPaths map[string]string `json:"backup_paths,omitempty"`
}

// Store manages the deploy history file.
type Store struct {
	Dir   string
	file  string
	index map[int]string // id → filename
	maxID int
	mu    sync.Mutex
}

// NewStore creates or opens a history store.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}

	s := &Store{
		Dir:   dir,
		file:  filepath.Join(dir, "index.json"),
		index: make(map[int]string),
	}

	// Load existing index
	data, err := os.ReadFile(s.file)
	if err == nil {
		var entries []struct {
			ID       int    `json:"id"`
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(data, &entries); err == nil {
			for _, e := range entries {
				s.index[e.ID] = e.Filename
				if e.ID > s.maxID {
					s.maxID = e.ID
				}
			}
		}
	}

	return s, nil
}

// Record saves a deploy entry and writes it to history.
func (s *Store) Record(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.maxID++
	entry.ID = s.maxID
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	filename := fmt.Sprintf("%d-%s.json", s.maxID, entry.Timestamp.Format("20060102-150405"))
	path := filepath.Join(s.Dir, filename)

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	// Update index
	s.index[entry.ID] = filename
	return s.saveIndex()
}

// List returns the most recent N entries.
func (s *Store) List(n int) ([]Entry, error) {
	// Sort IDs descending
	ids := make([]int, 0, len(s.index))
	for id := range s.index {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))

	if n <= 0 || n > len(ids) {
		n = len(ids)
	}
	if n > 20 {
		n = 20
	}
	ids = ids[:n]

	entries := make([]Entry, 0, n)
	for _, id := range ids {
		entry, err := s.Load(id)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Load loads a specific entry by ID.
func (s *Store) Load(id int) (Entry, error) {
	filename, ok := s.index[id]
	if !ok {
		return Entry{}, fmt.Errorf("entry %d not found", id)
	}

	path := filepath.Join(s.Dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read entry %d: %w", id, err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("parse entry %d: %w", id, err)
	}
	return entry, nil
}

// Latest returns the most recent entry.
func (s *Store) Latest() (Entry, error) {
	if s.maxID == 0 {
		return Entry{}, fmt.Errorf("no deploy history")
	}
	return s.Load(s.maxID)
}

func (s *Store) saveIndex() error {
	type idxEntry struct {
		ID       int    `json:"id"`
		Filename string `json:"filename"`
	}
	var entries []idxEntry
	for id, fn := range s.index {
		entries = append(entries, idxEntry{ID: id, Filename: fn})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}

	// Atomic write: temp file + rename (prevents concurrent-process corruption)
	tmpFile := s.file + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write index tmp: %w", err)
	}
	return os.Rename(tmpFile, s.file)
}

// FormatEntry formats an entry for human-readable display.
func FormatEntry(e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d] %s  %s  %s  %s", e.ID, e.Timestamp.Format("2006-01-02 15:04:05"),
		e.Operation, e.Source, e.Status)
	if e.Profile != "" {
		fmt.Fprintf(&b, "  profile:%s", e.Profile)
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "      Files: %d  Elapsed: %s  Path: %s\n", e.Files, e.Elapsed, e.RemotePath)
	if len(e.Servers) > 0 {
		fmt.Fprintf(&b, "      Servers: %s\n", strings.Join(e.Servers, ", "))
	}
	if e.Commit != "" {
		fmt.Fprintf(&b, "      Commit: %s\n", e.Commit)
	}
	if e.Notes != "" {
		fmt.Fprintf(&b, "      Notes: %s\n", e.Notes)
	}
	return b.String()
}
