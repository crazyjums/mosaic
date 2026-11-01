package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"net/url"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// 产物面板和聊天里的文件链接：查看任意文本类型（包含代码/脚本），
// 可用系统文档程序打开网页与图片，脚本只能用文本编辑器打开。
// 安全策略针对“执行”而非“查看”：预览不执行内容，文件关联可能执行脚本，
// 因此绝不直接将 .py/.bat/.ps1/.sh 等交给默认文件关联。
// 所有入口仍校验真实工作区路径/符号链接；预览有大小限制，HTML 在前端沙箱里展示。

// maxTextBytes 是产物预览最多读多少字节。
const maxTextBytes = 2 << 20

// safeDocumentTypes 可以用系统默认程序打开的文档，不能加入可能被文件关联“执行”的脚本/代码。
// 所有文本类型均可在应用内只读预览，源码使用明确的文本编辑器打开。
var safeDocumentTypes = map[string]bool{
	".html": true, ".htm": true, ".svg": true, ".md": true, ".markdown": true, ".txt": true,
	".mmd": true, ".mermaid": true, ".dot": true, ".gv": true, ".json": true, ".csv": true,
}

func canOpen(ext string) bool {
	if safeDocumentTypes[ext] || ext == ".pdf" {
		return true
	}
	_, ok := imageTypes[ext]
	return ok
}

// 即使文件恰好含合法 UTF-8，绝不能把这些扩展名当作安全的文档程序启动。
// 对它们只提供文件管理器中的定位，不做打开/执行。
func executableExtension(ext string) bool {
	switch ext {
	case ".exe", ".com", ".msi", ".msp", ".dll", ".so", ".dylib", ".app",
		".lnk", ".url", ".desktop", ".jar", ".apk", ".deb", ".rpm", ".scr":
		return true
	}
	return false
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
	real, fi, err := resolveWorkspaceFileWithAccess(workspace, p, allowOutside)
	if err != nil {
		return WorkspaceFile{}, err
	}
	f, err := os.Open(real)
	if err != nil {
		return WorkspaceFile{}, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxTextBytes+1))
	if err != nil {
		return WorkspaceFile{}, err
	}
	out := WorkspaceFile{Path: real, Size: fi.Size()}
	if len(buf) > maxTextBytes {
		buf, out.Truncated = buf[:maxTextBytes], true
	}
	// 限制按内容判断（而非扩展名）：源码、配置和任意纯文本都能预览；
	// 真正的二进制不能当成字符显示，更不会通过系统文件关联执行。
	value, err := decodePreviewText(buf)
	if err != nil {
		return WorkspaceFile{}, fmt.Errorf("无法将 %s 作为文本预览：%w", filepath.Base(real), err)
	}
	out.Text = value
	return out, nil
}

// Windows PowerShell 等工具会写 UTF-16 BOM；把它转换成 UTF-8 文本显示。
// 其他文件要求有效 UTF-8，避免把可执行文件或任意二进制误当文本。
func decodePreviewText(data []byte) (string, error) {
	if len(data) >= 2 && (bytes.Equal(data[:2], []byte{0xff, 0xfe}) || bytes.Equal(data[:2], []byte{0xfe, 0xff})) {
		littleEndian := data[0] == 0xff
		body := data[2:]
		if len(body)%2 != 0 {
			body = body[:len(body)-1] // 最后一个 UTF-16 字符可能被预览大小上限截断
		}
		values := make([]uint16, len(body)/2)
		for i := range values {
			if littleEndian {
				values[i] = binary.LittleEndian.Uint16(body[i*2:])
			} else {
				values[i] = binary.BigEndian.Uint16(body[i*2:])
			}
		}
		return string(utf16.Decode(values)), nil
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Errorf("文件不是 UTF-8/UTF-16 文本（可能是二进制），请在文件管理器中查看")
	}
	return string(data), nil
}

// OpenWorkspaceFile 是用户点击产物时的安全打开：文档由默认程序打开；
// 代码/脚本只用明确指定的文本编辑器打开；二进制和未知类型在文件管理器中定位。
// 与 agent 的权限审批完全独立，不通过 shell 执行文件内容。
func (a *App) OpenWorkspaceFile(path string) error {
	st := a.engine.Status()
	return openArtifactWithAccess(st.Workspace, path, st.PermissionMode == "auto", openFile, openInTextEditor, revealFile)
}

// RevealWorkspaceFile 允许用户在文件管理器中定位任意类型产物。
// 不执行文件，不允许使用符号链接绕过当前工作区边界。
func (a *App) RevealWorkspaceFile(path string) error {
	st := a.engine.Status()
	return revealWorkspaceFileWithAccess(st.Workspace, path, st.PermissionMode == "auto", revealFile)
}

func revealWorkspaceFileWithAccess(workspace, p string, allowOutside bool, reveal func(string) error) error {
	real, _, err := resolveWorkspaceFileWithAccess(workspace, cleanPath(p), allowOutside)
	if err != nil {
		return err
	}
	return reveal(real)
}

// openArtifactWithAccess 通过注入动作便于测试：不可信扩展名绝不走系统默认文件关联。
func openArtifactWithAccess(workspace, p string, allowOutside bool, open, edit, reveal func(string) error) error {
	real, _, err := resolveWorkspaceFileWithAccess(workspace, cleanPath(p), allowOutside)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(real))
	if canOpen(ext) {
		return open(real)
	}
	if executableExtension(ext) {
		return reveal(real)
	}
	// 不能按扩展名就把文件交给默认关联；先检查是否为实际文本。
	if _, err := readWorkspaceTextWithAccess(workspace, real, allowOutside); err == nil {
		return edit(real)
	}
	return reveal(real)
}

// openInTextEditor 选择受信任的文本编辑器，不读取用户可控的 EDITOR，也不经 shell。
// 系统没有可用 GUI 编辑器时退化为在文件管理器中定位。
func openInTextEditor(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("notepad.exe", path)
	case "darwin":
		cmd = exec.Command("open", "-a", "TextEdit", path)
	default:
		for _, name := range []string{"code", "codium", "gedit", "kate", "mousepad", "xed", "pluma", "leafpad"} {
			if _, err := exec.LookPath(name); err == nil {
				cmd = exec.Command(name, path)
				break
			}
		}
		if cmd == nil {
			return revealFile(path)
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法使用文本编辑器打开文件：%w", err)
	}
	go cmd.Wait()
	return nil
}

// revealFile 只打开文件管理器，不执行选中文件。
func revealFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,", path)
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法在文件管理器中定位文件：%w", err)
	}
	go cmd.Wait()
	return nil
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
