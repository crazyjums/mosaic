package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 产物面板和聊天里的文件链接：读出工作区里的文本文件做预览，或用系统默认程序打开（HTML 就是默认浏览器）。
//
// 安全：
//   - 只允许当前会话工作区之内的文件（解析符号链接后再检查，防止用链接指到工作区外）；
//   - 只认白名单里的扩展名。“用系统默认程序打开”在 Windows 上对 .bat / .exe / .lnk 等就是执行，
//     模型写出的文件不能借这个入口跑起来，所以白名单里只有文档、网页、图片、数据这类不会被执行的类型；
//   - 读文本有大小上限。

// maxTextBytes 是产物预览最多读多少字节。
const maxTextBytes = 2 << 20

// textTypes 可以读出来做预览的文本类型。
var textTypes = map[string]bool{
	".html": true, ".htm": true, ".svg": true, ".md": true, ".markdown": true, ".txt": true,
	".mmd": true, ".mermaid": true, ".dot": true, ".gv": true, ".json": true, ".csv": true,
}

// openTypes 可以用系统默认程序打开的类型：文本预览类型 + 图片 + PDF。
func canOpen(ext string) bool {
	if textTypes[ext] || ext == ".pdf" {
		return true
	}
	_, ok := imageTypes[ext]
	return ok
}

// WorkspaceFile 是 ReadWorkspaceText 的结果。
type WorkspaceFile struct {
	// Path 是文件的绝对路径（界面悬停时显示，让用户知道它在电脑上的哪里）。
	Path      string `json:"path"`
	Text      string `json:"text"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
}

// resolveWorkspaceFile 把相对工作区的路径（或工作区内的绝对路径）解析成真实路径，并确认它在工作区之内、是个文件。
func resolveWorkspaceFile(workspace, p string) (string, os.FileInfo, error) {
	if workspace == "" {
		return "", nil, fmt.Errorf("没有工作区")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, filepath.FromSlash(p))
	}
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", nil, err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", nil, fmt.Errorf("找不到文件 %s", p)
	}
	if rel, err := filepath.Rel(realWS, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("只能访问工作区内的文件")
	}
	fi, err := os.Stat(real)
	if err != nil || fi.IsDir() {
		return "", nil, fmt.Errorf("找不到文件 %s", p)
	}
	return real, fi, nil
}

func cleanPath(p string) string { return strings.TrimPrefix(strings.TrimSpace(p), "file://") }

// ReadWorkspaceText 读取当前会话工作区内的文本文件（网页、SVG、Markdown、Mermaid、DOT 等），供产物面板预览。
func (a *App) ReadWorkspaceText(path string) (WorkspaceFile, error) {
	return readWorkspaceText(a.engine.Status().Workspace, path)
}

func readWorkspaceText(workspace, p string) (WorkspaceFile, error) {
	p = cleanPath(p)
	if !textTypes[strings.ToLower(filepath.Ext(p))] {
		return WorkspaceFile{}, fmt.Errorf("不支持预览这种文件：%s", filepath.Base(p))
	}
	real, fi, err := resolveWorkspaceFile(workspace, p)
	if err != nil {
		return WorkspaceFile{}, err
	}
	f, err := os.Open(real)
	if err != nil {
		return WorkspaceFile{}, err
	}
	defer f.Close()
	buf := make([]byte, maxTextBytes+1)
	n, _ := f.Read(buf)
	out := WorkspaceFile{Path: real, Size: fi.Size()}
	if n > maxTextBytes {
		n, out.Truncated = maxTextBytes, true
	}
	out.Text = strings.ToValidUTF8(string(buf[:n]), "�")
	return out, nil
}

// OpenWorkspaceFile 用系统默认程序打开工作区内的文件：.html 会在默认浏览器里打开。
func (a *App) OpenWorkspaceFile(path string) error {
	return openWorkspaceFile(a.engine.Status().Workspace, path, openFile)
}

func openWorkspaceFile(workspace, p string, open func(string) error) error {
	p = cleanPath(p)
	if !canOpen(strings.ToLower(filepath.Ext(p))) {
		return fmt.Errorf("出于安全考虑，不能用系统程序打开这种文件：%s", filepath.Base(p))
	}
	real, _, err := resolveWorkspaceFile(workspace, p)
	if err != nil {
		return err
	}
	return open(real)
}

// openFile 用系统默认程序打开文件。
func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		// FileProtocolHandler 等价于在资源管理器里双击：.html 交给默认浏览器；不经过 cmd，路径里的特殊字符不会被解释。
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开文件：%w", err)
	}
	go cmd.Wait() // 回收子进程，避免僵尸进程
	return nil
}
