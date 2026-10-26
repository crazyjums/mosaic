package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 复制 / 解压插件包时的限制：包是别人给的，不能让一个压缩炸弹或者成千上万个文件拖垮机器。
const (
	maxPackEntries  = 3000
	maxPackBytes    = 100 << 20 // 全部文件解压后合计
	maxPackFileSize = 20 << 20  // 单个文件
)

// extractZip 把 zip 解压到 dst（必须是已存在的空目录）。
// 拒绝：绝对路径、带 .. 的路径（zip slip）、符号链接条目、超过数量和大小限制的包。
// 压缩包里只有一个顶层目录时（GitHub 下载的 zip 都是这样），调用方用 pluginRoot 找到清单所在的目录。
func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("不是有效的 zip 文件：%v", err)
	}
	defer zr.Close()
	if len(zr.File) > maxPackEntries {
		return fmt.Errorf("zip 里的文件太多（超过 %d 个）", maxPackEntries)
	}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || filepath.IsAbs(name) || (len(name) > 1 && name[1] == ':') {
			return fmt.Errorf("zip 里有不安全的路径：%q", name)
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("zip 里有不安全的路径：%q", name)
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("zip 里有符号链接 %q，出于安全不允许", name)
		}
		target := filepath.Join(dst, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.UncompressedSize64 > maxPackFileSize {
			return fmt.Errorf("zip 里的文件 %q 太大（超过 %d MB）", name, maxPackFileSize>>20)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// 不信任头部声明的大小：实际写入时再限制一次，同时累计总大小。
		n, err := writeLimited(f, target, modeOf(f.Mode()), maxPackFileSize, maxPackBytes-total)
		if err != nil {
			return err
		}
		if total += n; total > maxPackBytes {
			return fmt.Errorf("zip 解压后太大（超过 %d MB）", maxPackBytes>>20)
		}
	}
	return nil
}

func writeLimited(f *zip.File, target string, mode os.FileMode, perFile, remaining int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, fmt.Errorf("zip 里有重复或冲突的路径 %q", f.Name)
	}
	defer out.Close()
	limit := perFile
	if remaining < limit {
		limit = remaining
	}
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, errors.New("zip 里的文件超过大小限制")
	}
	return n, nil
}

// modeOf 保留可执行位（脚本要能运行），其余统一成普通文件权限。
func modeOf(m os.FileMode) os.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// copyDir 把插件目录复制到 dst（已存在的空目录）。跳过 .git；遇到符号链接直接拒绝：
// 链接可能指到插件目录之外，复制进来之后就成了一条“合法”的路径。
func copyDir(src, dst string) error {
	var entries int
	var total int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if entries++; entries > maxPackEntries {
			return fmt.Errorf("插件目录里的文件太多（超过 %d 个）", maxPackEntries)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("插件目录里有符号链接 %q，出于安全不允许", rel)
		case d.IsDir():
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		case !info.Mode().IsRegular():
			return fmt.Errorf("插件目录里有不支持的文件类型 %q", rel)
		}
		if info.Size() > maxPackFileSize {
			return fmt.Errorf("文件 %q 太大（超过 %d MB）", rel, maxPackFileSize>>20)
		}
		if total += info.Size(); total > maxPackBytes {
			return fmt.Errorf("插件目录太大（超过 %d MB）", maxPackBytes>>20)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, modeOf(info.Mode()))
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// pluginRoot 找到清单所在的目录：dir 下直接有 plugin.json，或者只有一个子目录且里面有。
func pluginRoot(dir string) (string, error) {
	if fileExists(filepath.Join(dir, FileName)) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var subs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.EqualFold(e.Name(), "__MACOSX") {
			subs = append(subs, e.Name())
		}
	}
	if len(subs) == 1 && fileExists(filepath.Join(dir, subs[0], FileName)) {
		return filepath.Join(dir, subs[0]), nil
	}
	return "", fmt.Errorf("找不到 %s（要放在插件目录或压缩包的最外层）", FileName)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
