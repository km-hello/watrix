package meter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"watrik/internal/config"
)

const maxResponseBytes = 1 << 20

var (
	ErrFetch    = errors.New("meter request failed")
	ErrHTTP     = errors.New("meter source returned an unsuccessful status")
	ErrTooLarge = errors.New("meter response exceeds size limit")
	ErrRedirect = errors.New("meter redirect rejected")
)

// Fetcher 只读取通过校验的电表地址，并限制超时、跳转和响应大小。
type Fetcher struct {
	url    string
	mid    string
	client *http.Client
}

// NewFetcher 校验采集地址并创建客户端；transport 为 nil 时使用默认传输实现。
func NewFetcher(rawURL string, transport http.RoundTripper) (*Fetcher, error) {
	u, err := config.ValidateMeterURL(rawURL)
	if err != nil {
		return nil, err
	}
	f := &Fetcher{url: u.String(), mid: u.Query().Get("mid")}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		// 每次跳转都重新校验来源和电表编号，防止离开允许的采集端点。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			u, err := config.ValidateMeterURL(req.URL.String())
			if err != nil || u.Query().Get("mid") != f.mid || len(via) >= 5 {
				return ErrRedirect
			}
			return nil
		},
	}
	return f, nil
}

// Fetch 获取并解析电表快照，失败时仅返回脱敏后的分类错误。
func (f *Fetcher) Fetch(ctx context.Context) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Snapshot{}, ErrFetch
	}
	response, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return Snapshot{}, ErrRedirect
		}
		// net/http 错误可能带有含电表编号的完整地址，不能直接向外传播。
		return Snapshot{}, ErrFetch
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Snapshot{}, ErrHTTP
	}
	if response.ContentLength > maxResponseBytes {
		return Snapshot{}, ErrTooLarge
	}
	// 多读一个字节才能识别未声明长度或声明不实的超大响应。
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, ErrFetch
	}
	if len(body) > maxResponseBytes {
		return Snapshot{}, ErrTooLarge
	}
	return Parse(bytes.NewReader(body), f.mid)
}
