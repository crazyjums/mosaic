package anthropic

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
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("unexpected request %s %v", r.URL, r.Header)
		}
		io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","type":"model"},{"id":"claude-haiku-5-5","type":"model"}],"has_more":false}`)
	}))
	defer srv.Close()

	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, APIKey: "k"})
	got, err := p.(provider.ModelLister).ListModels(context.Background())
	if err != nil || !slices.Equal(got, []string{"claude-opus-5-5", "claude-haiku-5-5"}) {
		t.Fatalf("got %v %v", got, err)
	}
}
