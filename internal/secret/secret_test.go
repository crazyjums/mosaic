package secret

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringWithMock(t *testing.T) {
	keyring.MockInit()
	k := NewKeyring()
	if err := k.Available(); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在应返回 ErrNotFound: %v", err)
	}
	if err := k.Set("x", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := k.Get("x"); v != "v" {
		t.Fatal(v)
	}
	if err := k.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete("x"); err != nil {
		t.Fatal("删除不存在的条目不应报错")
	}
}
