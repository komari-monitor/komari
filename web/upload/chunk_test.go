package upload

import (
	"bytes"
	"strings"
	"testing"
)

// newTestStore returns an isolated Store rooted in a temp directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Root: t.TempDir(), MaxSize: 32 * ChunkSize}
}

// TestValidUploadIDRejectsTraversal is the path-traversal guard: upload IDs
// are used to build filesystem paths, so anything that is not a bare UUID must
// be refused before it reaches filepath.Join.
func TestValidUploadIDRejectsTraversal(t *testing.T) {
	deny := []string{
		"",
		"..",
		"../escape",
		"../../etc/passwd",
		"/absolute/path",
		"a/b",
		"a\\b",
		".",
		"not-a-uuid",
		"00000000-0000-0000-0000-00000000000",
		"00000000-0000-0000-0000-0000000000000",
	}
	for _, id := range deny {
		if validUploadID(id) {
			t.Errorf("validUploadID(%q) = true, want false", id)
		}
	}

	allow := []string{
		"00000000-0000-0000-0000-000000000000",
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301",
	}
	for _, id := range allow {
		if !validUploadID(id) {
			t.Errorf("validUploadID(%q) = false, want true", id)
		}
	}
}

// TestStoreInitValidatesPurposeAndSize covers the entry-point validation.
func TestStoreInitValidatesPurposeAndSize(t *testing.T) {
	store := newTestStore(t)

	tests := []struct {
		name    string
		purpose Purpose
		size    int64
		wantErr bool
	}{
		{name: "valid backup", purpose: PurposeBackup, size: 1024},
		{name: "valid plugin", purpose: PurposePlugin, size: 1},
		{name: "valid theme", purpose: PurposeTheme, size: ChunkSize},
		{name: "unknown purpose", purpose: Purpose("../../etc"), size: 1024, wantErr: true},
		{name: "empty purpose", purpose: Purpose(""), size: 1024, wantErr: true},
		{name: "zero size", purpose: PurposeBackup, size: 0, wantErr: true},
		{name: "negative size", purpose: PurposeBackup, size: -1, wantErr: true},
		{name: "oversized", purpose: PurposeBackup, size: store.MaxSize + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := store.Init(tt.purpose, "archive.zip", tt.size)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if session.ID == "" {
				t.Fatal("session ID is empty")
			}
			if !validUploadID(session.ID) {
				t.Fatalf("session ID %q is not a valid UUID", session.ID)
			}
		})
	}
}

// TestSaveChunkRejectsWrongSize ensures a chunk that does not match the
// declared upload size is refused, so a client cannot corrupt the archive.
func TestSaveChunkRejectsWrongSize(t *testing.T) {
	store := newTestStore(t)
	session, err := store.Init(PurposeBackup, "archive.zip", 8)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := store.SaveChunk(session.ID, 0, strings.NewReader("too-long-data")); err == nil {
		t.Fatal("oversized chunk was accepted")
	}
	if err := store.SaveChunk(session.ID, 0, strings.NewReader("short")); err == nil {
		t.Fatal("undersized chunk was accepted")
	}
	if err := store.SaveChunk(session.ID, 0, strings.NewReader("exactly8")); err != nil {
		t.Fatalf("exact-size chunk rejected: %v", err)
	}
}

// TestSaveChunkRejectsInvalidIndex covers out-of-range chunk indices.
func TestSaveChunkRejectsInvalidIndex(t *testing.T) {
	store := newTestStore(t)
	session, err := store.Init(PurposeBackup, "archive.zip", 8)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, index := range []int64{-1, 1, 99} {
		if err := store.SaveChunk(session.ID, index, strings.NewReader("exactly8")); err == nil {
			t.Errorf("chunk index %d was accepted, want an error", index)
		}
	}
}

// TestSaveChunkRejectsUnknownUpload covers the not-found path and, critically,
// a traversal attempt disguised as an upload ID.
func TestSaveChunkRejectsUnknownUpload(t *testing.T) {
	store := newTestStore(t)
	for _, id := range []string{
		"00000000-0000-0000-0000-000000000000",
		"../../escape",
		"",
	} {
		if err := store.SaveChunk(id, 0, strings.NewReader("data")); err == nil {
			t.Errorf("SaveChunk(%q) succeeded, want an error", id)
		}
	}
}

// TestCancelRejectsTraversal makes sure Cancel cannot delete outside its root.
func TestCancelRejectsTraversal(t *testing.T) {
	store := newTestStore(t)
	for _, id := range []string{"../../escape", "", "not-a-uuid"} {
		if err := store.Cancel(id); err == nil {
			t.Errorf("Cancel(%q) succeeded, want an error", id)
		}
	}
}

// TestMergeRequiresEveryChunk verifies a missing chunk blocks the merge.
func TestMergeRequiresEveryChunk(t *testing.T) {
	store := newTestStore(t)
	size := ChunkSize + 4
	session, err := store.Init(PurposeBackup, "archive.zip", size)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := store.SaveChunk(session.ID, 0, bytes.NewReader(bytes.Repeat([]byte("a"), int(ChunkSize)))); err != nil {
		t.Fatalf("save chunk 0: %v", err)
	}
	if _, err := store.Merge(session.ID); err == nil {
		t.Fatal("merge succeeded with a missing chunk")
	}
	if err := store.SaveChunk(session.ID, 1, bytes.NewReader([]byte("tail"))); err != nil {
		t.Fatalf("save chunk 1: %v", err)
	}
	merged, err := store.Merge(session.ID)
	if err != nil {
		t.Fatalf("merge with every chunk: %v", err)
	}
	if merged.ArchivePath == "" {
		t.Fatal("merged session has no archive path")
	}
}

// TestExpectedChunkSize pins the chunk arithmetic including the final chunk.
func TestExpectedChunkSize(t *testing.T) {
	tests := []struct {
		size    int64
		index   int64
		want    int64
		wantErr bool
	}{
		{size: 1, index: 0, want: 1},
		{size: ChunkSize, index: 0, want: ChunkSize},
		{size: ChunkSize + 1, index: 0, want: ChunkSize},
		{size: ChunkSize + 1, index: 1, want: 1},
		{size: 2 * ChunkSize, index: 1, want: ChunkSize},
		{size: 2 * ChunkSize, index: 2, wantErr: true},
		{size: ChunkSize, index: -1, wantErr: true},
	}
	for _, tt := range tests {
		got, err := expectedChunkSize(tt.size, tt.index)
		if tt.wantErr {
			if err == nil {
				t.Errorf("expectedChunkSize(%d, %d) succeeded, want an error", tt.size, tt.index)
			}
			continue
		}
		if err != nil {
			t.Errorf("expectedChunkSize(%d, %d): %v", tt.size, tt.index, err)
			continue
		}
		if got != tt.want {
			t.Errorf("expectedChunkSize(%d, %d) = %d, want %d", tt.size, tt.index, got, tt.want)
		}
	}
}

// TestChunkCount pins the number of chunks for exact and partial multiples.
func TestChunkCount(t *testing.T) {
	tests := []struct{ size, want int64 }{
		{1, 1},
		{ChunkSize, 1},
		{ChunkSize + 1, 2},
		{2 * ChunkSize, 2},
		{2*ChunkSize + 1, 3},
	}
	for _, tt := range tests {
		if got := chunkCount(tt.size); got != tt.want {
			t.Errorf("chunkCount(%d) = %d, want %d", tt.size, got, tt.want)
		}
	}
}
