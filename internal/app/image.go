package app

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// 聊天里显示工作区中的图片：模型常常用 write_file 生成 SVG 效果图、流程图，再在回答里用
// Markdown 链接引用它。WebView 不能直接访问本地文件，所以由后端读出来，以 data URL 交给前端。
//
// 安全：只允许读当前会话工作区之内的图片（解析符号链接后再检查），只认图片扩展名，限制大小。
// SVG 以 data URL 放进 <img>，浏览器不会执行其中的脚本。

const maxImageBytes = 10 << 20

var imageTypes = map[string]string{
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp", ".ico": "image/x-icon",
}

// ReadWorkspaceImage 读取当前会话工作区内的图片，返回 data URL。path 可以是相对工作区的路径或工作区内的绝对路径。
func (a *App) ReadWorkspaceImage(path string) (string, error) {
	return readWorkspaceImage(a.engine.Status().Workspace, path)
}

func readWorkspaceImage(workspace, p string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("没有工作区")
	}
	p = strings.TrimPrefix(strings.TrimSpace(p), "file://")
	mime, ok := imageTypes[strings.ToLower(filepath.Ext(p))]
	if !ok {
		return "", fmt.Errorf("不支持的图片类型：%s", p)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, filepath.FromSlash(p))
	}
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("找不到图片 %s", p)
	}
	if rel, err := filepath.Rel(realWS, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("只能显示工作区内的图片")
	}
	fi, err := os.Stat(real)
	if err != nil || fi.IsDir() {
		return "", fmt.Errorf("找不到图片 %s", p)
	}
	if fi.Size() > maxImageBytes {
		return "", fmt.Errorf("图片太大（%d MB），超过 %d MB 的不在聊天里显示", fi.Size()>>20, maxImageBytes>>20)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func isWebURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
