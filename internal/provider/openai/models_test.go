package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"bad"}}`)
			return
		}
		io.WriteString(w, `{"object":"list","data":[{"id":"m-b"},{"id":"m-a"}]}`)
	}))
	defer srv.Close()

	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL + "/v1", APIKey: "k"})
	l, ok := p.(provider.ModelLister)
	if !ok {
		t.Fatal("openai 适配器应实现 ModelLister")
	}
	got, err := l.ListModels(context.Background())
	if err != nil || !slices.Equal(got, []string{"m-a", "m-b"}) {
		t.Fatalf("got %v %v", got, err)
	}

	bad, _ := New(provider.ProfileConfig{BaseURL: srv.URL + "/v1", APIKey: "wrong"})
	if _, err := bad.(provider.ModelLister).ListModels(context.Background()); err == nil {
		t.Fatal("密钥错误应返回错误")
	}
}
