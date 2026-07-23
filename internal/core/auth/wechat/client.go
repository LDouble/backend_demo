// Package wechat provides a minimal WeChat Mini Program authentication client.
//
// The client only wraps the jscode2session endpoint and never speaks
// higher-level APIs (encryptedData decryption, payments, etc.). It is
// intentionally SDK-free to keep the dependency surface small and to
// ensure error handling stays in the platform's apperror envelope.
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
)

// Session is the minimal payload returned by jscode2session.
//
// UnionID is empty when the Mini Program is not bound to a WeChat Open Platform
// account. SessionKey is preserved verbatim so future flows (encryptedData
// decryption) can consume it without re-hitting the WeChat API.
type Session struct {
	OpenID     string
	UnionID    string
	SessionKey string
}

// Client exchanges a Mini Program js_code for a WeChat session.
type Client interface {
	Code2Session(ctx context.Context, appid, code string) (Session, error)
}

// SecretsResolver returns the active appid -> secret map.
//
// Implementations must be safe to call concurrently. A nil or empty map means
// no Mini Program is currently configured; every Code2Session call will fail
// with invalid_wechat_code until the resolver returns a usable map.
type SecretsResolver interface {
	Resolve() map[string]string
}

// HTTPClient is the default production implementation.
//
// Each call requires an appid because a single deployment may serve several
// Mini Programs. The matching secret is resolved from the supplied resolver
// at call time so that operators can rotate secrets through the configuration
// center without restarting the service. An unknown or missing appid produces
// an invalid_wechat_code error without leaking the WeChat response body to
// the client.
type HTTPClient struct {
	endpoint string
	timeout  time.Duration
	hc       *http.Client
	secrets  SecretsResolver
}

// NewHTTPClient builds a Client pinned to the given endpoint, with per-call
// HTTP timeouts and a runtime secret resolver. resolver must not be nil.
func NewHTTPClient(endpoint string, timeout time.Duration, resolver SecretsResolver) *HTTPClient {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://api.weixin.qq.com"
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if resolver == nil {
		resolver = emptySecretsResolver{}
	}
	return &HTTPClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		timeout:  timeout,
		hc:       &http.Client{Timeout: timeout},
		secrets:  resolver,
	}
}

// Code2Session implements Client.
func (c *HTTPClient) Code2Session(ctx context.Context, appid, code string) (Session, error) {
	appid = strings.TrimSpace(appid)
	code = strings.TrimSpace(code)
	if appid == "" {
		return Session{}, apperror.New(http.StatusBadRequest, "invalid_request", "appid is required")
	}
	if code == "" {
		return Session{}, apperror.New(http.StatusBadRequest, "invalid_request", "code is required")
	}
	secret := c.resolveSecret(appid)
	if strings.TrimSpace(secret) == "" {
		return Session{}, apperror.New(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	target := fmt.Sprintf("%s/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		c.endpoint, url.QueryEscape(appid), url.QueryEscape(secret), url.QueryEscape(code))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return Session{}, apperror.Wrap(http.StatusInternalServerError, "wechat_request_failed", "微信登录请求构造失败", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "campus-platform/1.0")
	resp, err := c.hc.Do(req)
	if err != nil {
		return Session{}, apperror.Wrap(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Session{}, apperror.New(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if err != nil {
		return Session{}, apperror.Wrap(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败", err)
	}
	var raw jscodeResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return Session{}, apperror.Wrap(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败", err)
	}
	if raw.ErrCode != 0 {
		return Session{}, apperror.New(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败")
	}
	if raw.OpenID == "" {
		return Session{}, apperror.New(http.StatusUnauthorized, "invalid_wechat_code", "微信登录失败")
	}
	return Session{OpenID: raw.OpenID, UnionID: raw.UnionID, SessionKey: raw.SessionKey}, nil
}

func (c *HTTPClient) resolveSecret(appid string) string {
	secrets := c.secrets.Resolve()
	if secrets == nil {
		return ""
	}
	return secrets[appid]
}

// emptySecretsResolver satisfies SecretsResolver with an empty map.
type emptySecretsResolver struct{}

func (emptySecretsResolver) Resolve() map[string]string { return nil }

// jscodeResponse matches the WeChat jscode2session JSON payload.
type jscodeResponse struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// ErrUnknownAppID is returned by callers that want to distinguish a missing
// appid configuration from a network error. The HTTP client itself hides the
// difference to avoid leaking configuration state.
var ErrUnknownAppID = errors.New("wechat: appid not configured")
