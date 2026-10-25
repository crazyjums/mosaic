package sysproc

import (
	"os/exec"
	"reflect"
	"runtime"
	"testing"
)

// 在 Windows 上检查进程创建标志；其他平台上只检查调用是安全的。
// 用反射读字段，因为 SysProcAttr 的结构在各平台不同，测试文件不能直接写 CreationFlags。
func TestHideConsole(t *testing.T) {
	cmd := exec.Command("git", "--version")
	HideConsole(cmd)
	if runtime.GOOS != "windows" {
		return
	}
	attr := reflect.ValueOf(cmd.SysProcAttr).Elem()
	if !attr.FieldByName("HideWindow").Bool() {
		t.Error("HideWindow 应为 true")
	}
	if attr.FieldByName("CreationFlags").Uint()&0x08000000 == 0 {
		t.Error("应设置 CREATE_NO_WINDOW")
	}
}
