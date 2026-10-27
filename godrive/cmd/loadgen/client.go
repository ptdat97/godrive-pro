package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// client gọi HTTP API như một app thật. Mọi lời gọi trả kèm độ trễ để đo.
type client struct {
	base string
	hc   *http.Client
}

func newClient(base string) *client {
	return &client{base: base, hc: &http.Client{
		Timeout: 15 * time.Second,
		// Mặc định chỉ giữ 2 kết nối rảnh mỗi host: dưới tải, mỗi request lại
		// bắt tay TCP mới và cái được đo là việc mở kết nối, không phải API.
		Transport: &http.Transport{MaxIdleConns: 512, MaxIdleConnsPerHost: 512, IdleConnTimeout: time.Minute},
	}}
}

// apiError là lỗi do backend trả về, giữ mã lỗi ổn định để thống kê.
type apiError struct {
	Status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Msg) }

// errCode lấy mã lỗi để đếm; lỗi mạng gom chung một nhóm.
func errCode(err error) string {
	if e, ok := err.(*apiError); ok {
		return e.Code
	}
	return "network"
}

func (c *client) call(ctx context.Context, method, path, tok string, body, out any, hdr ...string) (time.Duration, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	t := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return time.Since(t), err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	d := time.Since(t)
	if err != nil {
		return d, err
	}
	if resp.StatusCode >= 400 {
		var e struct{ Code, Message string }
		_ = json.Unmarshal(raw, &e)
		return d, &apiError{Status: resp.StatusCode, Code: e.Code, Msg: e.Message}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return d, fmt.Errorf("giải mã %s: %w", path, err)
		}
	}
	return d, nil
}

type session struct {
	Token     string
	AccountID string
}

func (c *client) login(ctx context.Context, phone, role string) (session, error) {
	var ch struct {
		ChallengeID string `json:"challenge_id"`
		DevCode     string `json:"dev_code"`
	}
	if _, err := c.call(ctx, "POST", "/v1/auth/otp", "", map[string]string{"phone": phone, "role": role}, &ch); err != nil {
		return session{}, err
	}
	if ch.DevCode == "" {
		return session{}, fmt.Errorf("backend không trả dev_code — cần DEV_AUTH=true")
	}
	var tp struct {
		AccessToken string `json:"access_token"`
		Account     struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if _, err := c.call(ctx, "POST", "/v1/auth/verify", "", map[string]string{
		"challenge_id": ch.ChallengeID, "code": ch.DevCode, "device_id": "loadgen",
	}, &tp); err != nil {
		return session{}, err
	}
	return session{Token: tp.AccessToken, AccountID: tp.Account.ID}, nil
}

func (c *client) adminLogin(ctx context.Context, phone string) (string, error) {
	var ch struct {
		ChallengeID string `json:"challenge_id"`
		DevCode     string `json:"dev_code"`
	}
	if _, err := c.call(ctx, "POST", "/v1/admin/auth/otp", "", map[string]string{"phone": phone}, &ch); err != nil {
		return "", err
	}
	var tp struct {
		AccessToken string `json:"access_token"`
	}
	if _, err := c.call(ctx, "POST", "/v1/admin/auth/verify", "", map[string]string{
		"challenge_id": ch.ChallengeID, "code": ch.DevCode, "device_id": "loadgen",
	}, &tp); err != nil {
		return "", err
	}
	return tp.AccessToken, nil
}

// parallel chạy fn cho i = 0..n-1 với tối đa k việc cùng lúc, dừng ở lỗi đầu tiên.
func parallel(ctx context.Context, n, k int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, k)
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, i); err != nil {
				once.Do(func() { first = fmt.Errorf("#%d: %w", i, err); cancel() })
			}
		}(i)
	}
	wg.Wait()
	return first
}
