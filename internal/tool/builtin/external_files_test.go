package builtin

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/crazyjums/myllmapp/internal/tool"
)

func TestAutoModeExternalFileTools(t *testing.T) {
    ws, outside := t.TempDir(), t.TempDir()
    target := filepath.Join(outside, "report.html")
    if err := os.WriteFile(target, []byte("external document"), 0o644); err != nil { t.Fatal(err) }
    reg := newRegistry()
    read, _ := reg.Get("read_file")
    input, _ := json.Marshal(map[string]string{"path": target})
    call := tool.Call{Workspace: ws, Input: input}
    if r := read.Execute(context.Background(), call); !r.IsError {
        t.Fatalf("default mode should reject an outside file: %+v", r)
    }
    call.AllowOutsideWorkspace = true
    if r := read.Execute(context.Background(), call); r.IsError || !strings.Contains(r.Output, "external document") {
        t.Fatalf("auto mode should read the file: %+v", r)
    }
    write, _ := reg.Get("write_file")
    another := filepath.Join(outside, "new.html")
    call.Input, _ = json.Marshal(map[string]string{"path": another, "content": "<h1>report</h1>"})
    if r := write.Execute(context.Background(), call); r.IsError {
        t.Fatalf("auto mode should write outside workspace: %+v", r)
    }
    if b, err := os.ReadFile(another); err != nil || string(b) != "<h1>report</h1>" {
        t.Fatalf("outside file was not written: %q %v", b, err)
    }
    call.AllowOutsideWorkspace = false
    call.Input, _ = json.Marshal(map[string]string{"path": target})
    if r := read.Execute(context.Background(), call); !r.IsError {
        t.Fatalf("file access must be restricted after switching away from auto: %+v", r)
    }
}
