package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidUploadID(t *testing.T) {
	for _, uploadID := range []string{chunkUploadID, "../backup", "", "another-upload"} {
		want := uploadID == chunkUploadID
		if got := isValidUploadID(uploadID); got != want {
			t.Errorf("isValidUploadID(%q) = %v, want %v", uploadID, got, want)
		}
	}
}

func TestChunkUploadDirRejectsTraversal(t *testing.T) {
	if _, err := chunkUploadDir(".."); err == nil {
		t.Fatal("chunkUploadDir accepted traversal upload_id")
	}
}

func TestClearChunkUploadCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "interrupted.part"), []byte("stale"), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	if err := clearChunkUploadCache(root); err != nil {
		t.Fatalf("clearChunkUploadCache: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache entries = %d, want 0", len(entries))
	}
}

func TestCopyWhitelistedFilesIncludesFont(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	fontPath := filepath.Join(dataDir, "font.ttf")
	if err := os.WriteFile(fontPath, []byte("font-data"), 0644); err != nil {
		t.Fatalf("write font: %v", err)
	}

	if err := copyWhitelistedFilesFrom(dataDir, backupDir); err != nil {
		t.Fatalf("copyWhitelistedFilesFrom: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(backupDir, "font.ttf"))
	if err != nil {
		t.Fatalf("read copied font: %v", err)
	}
	if string(content) != "font-data" {
		t.Fatalf("font content = %q, want %q", content, "font-data")
	}
}

func TestChunkUploadMetadataAndSize(t *testing.T) {
	dir := t.TempDir()
	metadata, err := json.Marshal(chunkUploadMetadata{Size: 7})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, uploadMetadataName), metadata, 0600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	for name, content := range map[string]string{"0.part": "abc", "1.part": "defg", ".1-staging.part": "staging", "ignored.txt": "ignored"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	gotMetadata, err := readChunkUploadMetadata(dir)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if gotMetadata.Size != 7 {
		t.Fatalf("metadata size = %d, want 7", gotMetadata.Size)
	}
	gotSize, err := chunkUploadSize(dir)
	if err != nil {
		t.Fatalf("chunkUploadSize: %v", err)
	}
	if gotSize != 7 {
		t.Fatalf("chunk upload size = %d, want 7", gotSize)
	}
}

func TestMergeChunksIgnoresTemporaryParts(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"0.part": "first", "1.part": "second", ".1-staging.part": "temporary"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	destination := filepath.Join(dir, "merged.zip")
	if err := mergeChunks(dir, destination); err != nil {
		t.Fatalf("mergeChunks: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read merged chunks: %v", err)
	}
	if string(content) != "firstsecond" {
		t.Fatalf("merged chunks = %q, want %q", content, "firstsecond")
	}
}
