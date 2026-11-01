package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"net/url"
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
	return resolveWorkspaceFileWithAccess(workspace, p, false)
}

// resolveWorkspaceFileWithAccess: auto 权限模式可访问工作区外的文档；普通模式仍严格限制在工作区。
func resolveWorkspaceFileWithAccess(workspace, p string, allowOutside bool) (string, os.FileInfo, error) {
	if workspace == "" && !filepath.IsAbs(p) {
		return "", nil, fmt.Errorf("没有工作区")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, filepath.FromSlash(p))
	}
	var realWS string
	if !allowOutside {
		var err error
		realWS, err = filepath.EvalSymlinks(workspace)
		if err != nil {
			return "", nil, err
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", nil, fmt.Errorf("找不到文件 %s", p)
	}
	if rel, err := filepath.Rel(realWS, real); !allowOutside && (err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", nil, fmt.Errorf("只能访问工作区内的文件")
	}
	fi, err := os.Stat(real)
	if err != nil || fi.IsDir() {
		return "", nil, fmt.Errorf("找不到文件 %s", p)
	}
	return real, fi, nil
}

// cleanPath 识别 file URL（含 URL 编码、Windows 盘符、UNC 共享目录），普通文件路径原样保留。
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(strings.ToLower(p), "file://") {
		return p
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "file" {
		return p
	}
	path, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return p
	}
	if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

// ReadWorkspaceText 读取当前会话工作区内的文本文件（网页、SVG、Markdown、Mermaid、DOT 等），供产物面板预览。
func (a *App) ReadWorkspaceText(path string) (WorkspaceFile, error) {
	st := a.engine.Status()
	return readWorkspaceTextWithAccess(st.Workspace, path, st.PermissionMode == "auto")
}

func readWorkspaceText(workspace, p string) (WorkspaceFile, error) {
	return readWorkspaceTextWithAccess(workspace, p, false)
}

func readWorkspaceTextWithAccess(workspace, p string, allowOutside bool) (WorkspaceFile, error) {
	p = cleanPath(p)
	if !textTypes[strings.ToLower(filepath.Ext(p))] {
		return WorkspaceFile{}, fmt.Errorf("不支持预览这种文件：%s", filepath.Base(p))
	}
	real, fi, err := resolveWorkspaceFileWithAccess(workspace, p, allowOutside)
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
	st := a.engine.Status()
	return openWorkspaceFileWithAccess(st.Workspace, path, st.PermissionMode == "auto", openFile)
}

func openWorkspaceFile(workspace, p string, open func(string) error) error {
	return openWorkspaceFileWithAccess(workspace, p, false, open)
}

func openWorkspaceFileWithAccess(workspace, p string, allowOutside bool, open func(string) error) error {
	p = cleanPath(p)
	if !canOpen(strings.ToLower(filepath.Ext(p))) {
		return fmt.Errorf("出于安全考虑，不能用系统程序打开这种文件：%s", filepath.Base(p))
	}
	real, _, err := resolveWorkspaceFileWithAccess(workspace, p, allowOutside)
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
