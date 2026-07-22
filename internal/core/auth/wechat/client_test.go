package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
)

// staticResolver implements SecretsResolver with a fixed map.
type staticResolver struct {
	mu     sync.RWMutex
	values map[string]string
}

func (s *staticResolver) Resolve() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

func appErrCode(err error) string {
	if appErr, ok := apperror.As(err); ok {
		return appErr.Code
	}
	return ""
}

func newTestClient(handler http.HandlerFunc) (*HTTPClient, *httptest.Server) {
	server := httptest.NewServer(handler)
	resolver := &staticResolver{values: map[string]string{
		"wxapp-1": "secret-1",
	}}
	client := NewHTTPClient(server.URL, 2*time.Second, resolver)
	return client, server
}

func TestCode2SessionSuccess(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sns/jscode2session") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("appid") != "wxapp-1" || r.URL.Query().Get("secret") != "secret-1" {
			http.Error(w, "mismatch", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("grant_type") != "authorization_code" {
			http.Error(w, "missing grant_type", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openid":"oX1","unionid":"uX1","session_key":"sk-1","errcode":0,"errmsg":"ok"}`))
	})
	defer server.Close()

	session, err := client.Code2Session(context.Background(), "wxapp-1", "code-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.OpenID != "oX1" || session.UnionID != "uX1" || session.SessionKey != "sk-1" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestCode2SessionUnknownAppID(t *testing.T) {
	client, server := newTestClient(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be called for unknown appid")
	})
	defer server.Close()
	if _, err := client.Code2Session(context.Background(), "wxapp-missing", "code"); err == nil {
		t.Fatal("expected error for unknown appid")
	} else if code := appErrCode(err); code != "invalid_wechat_code" {
		t.Fatalf("expected invalid_wechat_code, got %q (err=%v)", code, err)
	}
}

func TestCode2SessionRejectsErrorCode(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	})
	defer server.Close()
	_, err := client.Code2Session(context.Background(), "wxapp-1", "bad")
	if err == nil {
		t.Fatal("expected error for invalid code")
	}
	if code := appErrCode(err); code != "invalid_wechat_code" {
		t.Fatalf("expected invalid_wechat_code, got %q (err=%v)", code, err)
	}
}

func TestCode2SessionRequiresInput(t *testing.T) {
	client, server := newTestClient(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be called when input is invalid")
	})
	defer server.Close()
	if _, err := client.Code2Session(context.Background(), "", "code"); err == nil {
		t.Fatal("expected error when appid is empty")
	}
	if _, err := client.Code2Session(context.Background(), "wxapp-1", " "); err == nil {
		t.Fatal("expected error when code is blank")
	}
}

func TestCode2SessionTimeoutMapped(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"openid":"o","errcode":0}`))
	})
	defer server.Close()
	client.hc.Timeout = 20 * time.Millisecond
	_, err := client.Code2Session(context.Background(), "wxapp-1", "code")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if code := appErrCode(err); code != "invalid_wechat_code" {
		t.Fatalf("expected invalid_wechat_code, got %q (err=%v)", code, err)
	}
}

func TestCode2SessionNonOKStatus(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	defer server.Close()
	_, err := client.Code2Session(context.Background(), "wxapp-1", "code")
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if code := appErrCode(err); code != "invalid_wechat_code" {
		t.Fatalf("expected invalid_wechat_code, got %q (err=%v)", code, err)
	}
}

func TestCode2SessionMalformedJSON(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	})
	defer server.Close()
	_, err := client.Code2Session(context.Background(), "wxapp-1", "code")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
