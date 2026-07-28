package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if store.Dir != dir {
		t.Errorf("Dir = %q, want %q", store.Dir, dir)
	}
	if store.file != filepath.Join(dir, "index.json") {
		t.Errorf("file = %q, want %q", store.file, filepath.Join(dir, "index.json"))
	}
}

func TestRecordAndList(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	entry := Entry{
		Operation: "push",
		Method:    "git-diff",
		Servers:   []string{"prod"},
		Files:     5,
		Status:    "ok",
		Elapsed:   "1.2s",
	}

	err = store.Record(entry)
	if err != nil {
		t.Fatalf("Record() error: %v", err)
	}

	if store.maxID != 1 {
		t.Errorf("maxID = %d, want 1", store.maxID)
	}

	entries, err := store.List(1)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ID != 1 {
		t.Errorf("ID = %d, want 1", entries[0].ID)
	}
	if entries[0].Operation != "push" {
		t.Errorf("Operation = %q, want push", entries[0].Operation)
	}
}

func TestRecordMultipleAndListOrder(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	for i := 0; i < 5; i++ {
		store.Record(Entry{
			Operation: "push",
			Files:     i,
			Status:    "ok",
		})
	}

	entries, err := store.List(3)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// Should be newest first (5, 4, 3)
	expected := []int{5, 4, 3}
	for i, e := range entries {
		if e.ID != expected[i] {
			t.Errorf("entries[%d].ID = %d, want %d", i, e.ID, expected[i])
		}
	}
}

func TestListLimit(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	for i := 0; i < 30; i++ {
		store.Record(Entry{Operation: "push", Files: i, Status: "ok"})
	}

	entries, err := store.List(100) // should cap at 20
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) > 20 {
		t.Errorf("len(entries) = %d, want max 20", len(entries))
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Record(Entry{
		Operation: "push",
		Method:    "git-diff",
		Servers:   []string{"prod", "staging"},
		Files:     10,
		Status:    "ok",
	})

	entry, err := store.Load(1)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if entry.ID != 1 {
		t.Errorf("ID = %d, want 1", entry.ID)
	}
	if len(entry.Servers) != 2 {
		t.Errorf("len(Servers) = %d, want 2", len(entry.Servers))
	}
}

func TestLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	_, err := store.Load(42)
	if err == nil {
		t.Fatal("Expected error for nonexistent entry")
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Record(Entry{Operation: "push", Files: 1, Status: "ok"})
	store.Record(Entry{Operation: "push", Files: 2, Status: "ok"})

	entry, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if entry.ID != 2 {
		t.Errorf("ID = %d, want 2", entry.ID)
	}
	if entry.Files != 2 {
		t.Errorf("Files = %d, want 2", entry.Files)
	}
}

func TestLatestEmpty(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	_, err := store.Latest()
	if err == nil {
		t.Fatal("Expected error for empty store")
	}
}

func TestTimestampPreserved(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	store.Record(Entry{
		Operation: "push",
		Timestamp: ts,
		Status:    "ok",
	})

	entry, err := store.Load(1)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !entry.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", entry.Timestamp, ts)
	}
}

func TestPersistAcrossStores(t *testing.T) {
	dir := t.TempDir()

	store1, _ := NewStore(dir)
	store1.Record(Entry{Operation: "push", Files: 3, Status: "ok"})
	store1.Record(Entry{Operation: "pull", Files: 5, Status: "ok"})

	// Re-open from same directory
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	entries, err := store2.List(10)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
}

func TestBackupPaths(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Record(Entry{
		Operation: "push",
		Status:    "ok",
		BackupPaths: map[string]string{
			"prod": "/var/www/.deploi-backups/12345",
		},
	})

	entry, err := store.Load(1)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if entry.BackupPaths["prod"] != "/var/www/.deploi-backups/12345" {
		t.Errorf("BackupPaths = %v", entry.BackupPaths)
	}
}

func TestFormatEntry(t *testing.T) {
	entry := Entry{
		ID:         1,
		Timestamp:  time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC),
		Operation:  "push",
		Source:     "git-diff",
		Status:     "ok",
		Files:      5,
		Elapsed:    "2.1s",
		RemotePath: "/var/www",
		Servers:    []string{"prod", "staging"},
		Commit:     "abc1234",
		Notes:      "test deploy",
		Profile:    "full",
	}

	output := FormatEntry(entry)
	if output == "" {
		t.Fatal("FormatEntry returned empty string")
	}

	for _, s := range []string{"[1]", "push", "git-diff", "ok", "5", "abc1234", "test deploy", "full"} {
		if !stringsContains(output, s) {
			t.Errorf("FormatEntry missing %q\n%s", s, output)
		}
	}
}

func TestFormatEntryMinimal(t *testing.T) {
	entry := Entry{
		ID:        1,
		Timestamp: time.Now(),
		Operation: "push",
		Status:    "ok",
	}

	output := FormatEntry(entry)
	if !stringsContains(output, "[1]") {
		t.Errorf("FormatEntry missing ID\n%s", output)
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && stringsIndex(s, substr) >= 0
}

func stringsIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestStoreCreateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "history")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	err = store.Record(Entry{Operation: "test", Status: "ok"})
	if err != nil {
		t.Fatalf("Record() error: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("history dir not created: %v", err)
	}
}
