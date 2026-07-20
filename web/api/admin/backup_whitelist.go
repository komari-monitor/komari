package admin

import (
	"fmt"
	"os"
	"path/filepath"
)

// backupWhitelist 定义需要备份的 ./data/ 下文件/目录（相对路径）。
// 目录项会递归包含其下所有文件。新增需持久化数据时在此追加。
//
// 注意：komari.db 不在白名单中——database 通过 DownloadBackup 中的
// SQLite VACUUM INTO 或 copyFile 单独备份，确保一致性快照。
var backupWhitelist = []string{
	"favicon.ico",
	"theme/",
	"metrics.db",
}

// copyWhitelistedFiles 将白名单中存在的文件/目录复制到临时目录。
// 不存在的项静默跳过，确保白名单扩展的向前兼容。
func copyWhitelistedFiles(tempDir string) error {
	for _, relPath := range backupWhitelist {
		src := filepath.Join(".", "data", relPath)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat whitelist entry %s: %v", relPath, err)
		}
		if info.IsDir() {
			if err := copyDir(src, filepath.Join(tempDir, relPath)); err != nil {
				return fmt.Errorf("copy whitelist dir %s: %v", relPath, err)
			}
		} else {
			if err := copyFile(src, filepath.Join(tempDir, relPath)); err != nil {
				return fmt.Errorf("copy whitelist file %s: %v", relPath, err)
			}
		}
	}
	return nil
}

// copyDir 递归复制目录到目标路径。
func copyDir(srcDir, dstDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		return copyFile(path, dst)
	})
}
