package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsValidUploadID(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef"
	for _, uploadID := range []string{valid, "../" + valid, "", "not-a-valid-upload-id", valid + "00"} {
		want := uploadID == valid
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

func TestCleanupExpiredChunkUploadsPreservesActiveUploads(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	activeDir := filepath.Join(root, "active")
	for _, dir := range []string{oldDir, activeDir} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	now := time.Now()
	oldTime := now.Add(-uploadExpiration - time.Minute)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("age old upload: %v", err)
	}

	if err := cleanupExpiredChunkUploads(root, now); err != nil {
		t.Fatalf("cleanupExpiredChunkUploads: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired upload still exists: %v", err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("active upload was removed: %v", err)
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
	for name, content := range map[string]string{"0.part": "abc", "1.part": "defg", "ignored.txt": "ignored"} {
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
