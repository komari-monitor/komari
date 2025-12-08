package restore

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var backupZipPath = filepath.Join(".", "data", "backup.zip")

func NeedBackupRestore() bool {
	if _, statErr := os.Stat(backupZipPath); statErr == nil {
		return true
	}
	return false
}

func RestoreBackup() error {
	// 4. 把除了 ./data/backup.zip 之外的所有文件压缩到 ./backup/{time}.zip
	if err := os.MkdirAll("./backup", 0755); err != nil {
		log.Printf("[restore] failed to create backup dir: %v", err)
	} else {
		tsName := time.Now().Format("20060102-150405")
		bakPath := filepath.Join("./backup", fmt.Sprintf("%s.zip", tsName))
		if zipErr := zipDirectoryExcluding("./data", bakPath, map[string]struct{}{backupZipPath: {}}); zipErr != nil {
			return fmt.Errorf("[restore] failed to zip current data: %w", zipErr)
		} else {
			log.Printf("[restore] current data zipped to %s", bakPath)
		}
	}

	// 5. 删除除了 ./data/backup.zip 之外的所有文件
	if delErr := removeAllInDirExcept("./data", map[string]struct{}{backupZipPath: {}}); delErr != nil {
		return fmt.Errorf("[restore] failed to cleanup data dir: %w", delErr)
	}

	// 6. 解压 ./data/backup.zip 到 ./data
	if unzipErr := unzipToDir(backupZipPath, "./data"); unzipErr != nil {
		return fmt.Errorf("[restore] failed to unzip backup into data: %w", unzipErr)
	} else {
		log.Printf("[restore] backup.zip extracted to ./data")
	}

	// 7. 删除 ./data/backup.zip
	if rmErr := os.Remove(backupZipPath); rmErr != nil {
		return fmt.Errorf("[restore] failed to remove backup.zip: %w", rmErr)
	} else {
		log.Printf("[restore] backup.zip removed")
	}
	// 8. 删除标记
	if rmErr := os.Remove("./data/komari-backup-markup"); rmErr != nil {
		return fmt.Errorf("[restore] failed to remove komari-backup-markup: %w", rmErr)
	} else {
		log.Printf("[restore] komari-backup-markup removed")
	}
	return nil

}

// zipDirectoryExcluding 将 srcDir 打包为 dstZip，exclude 是绝对路径集合需要排除
func zipDirectoryExcluding(srcDir, dstZip string, exclude map[string]struct{}) error {
	// 规范化排除路径为绝对路径
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}

	out, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	absSrc, _ := filepath.Abs(srcDir)
	walkErr := filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 排除 backup.zip 本身
		if _, ok := normExclude[path]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 计算 zip 内相对路径
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		// 根目录跳过
		if rel == "." {
			return nil
		}
		// 替换为正斜杠
		zipName := filepath.ToSlash(rel)

		if info.IsDir() {
			_, err := zw.Create(zipName + "/")
			return err
		}
		// 普通文件
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(zipName)
		if err != nil {
			fh.Close()
			return err
		}
		if _, err := io.Copy(w, fh); err != nil {
			fh.Close()
			return err
		}
		fh.Close()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	return zw.Close()
}

// removeAllInDirExcept 删除 dir 下除 exclude 指定绝对路径外的所有文件和文件夹
func removeAllInDirExcept(dir string, exclude map[string]struct{}) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(absDir, e.Name())
		if _, ok := normExclude[full]; ok {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	return nil
}

// unzipToDir 将 zipPath 解压到 dstDir，包含路径遍历保护
func unzipToDir(zipPath, dstDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	absDst, _ := filepath.Abs(dstDir)

	for _, f := range zr.File {
		// 构造目标路径并做路径遍历保护
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join(absDst, cleanName)
		if !strings.HasPrefix(targetPath, absDst+string(os.PathSeparator)) && targetPath != absDst {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}
