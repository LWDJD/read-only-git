package arweave

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestUploadSendsSignedBytes(t *testing.T) {
	var (
		gotPath string
		gotType string
		gotBody []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc123"}`))
	}))
	defer srv.Close()

	u := NewUploader(srv.URL)
	id, err := u.Upload(context.Background(), []byte("signed-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc123" {
		t.Fatalf("id = %q", id)
	}
	if gotPath != "/tx" {
		t.Errorf("路径应为 /tx，实际 %q", gotPath)
	}
	if gotType != "application/octet-stream" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if string(gotBody) != "signed-bytes" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestUploadAcceptsCapitalizedID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Id":"CAPS"}`))
	}))
	defer srv.Close()

	id, err := NewUploader(srv.URL).Upload(context.Background(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "CAPS" {
		t.Fatalf("id = %q", id)
	}
}

func TestUploadSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad data item", http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := NewUploader(srv.URL).Upload(context.Background(), []byte("x")); err == nil {
		t.Fatal("4xx 应当报错")
	}
}

func TestUploadRejectsEmptyPayload(t *testing.T) {
	// 空载荷在发请求之前就该被挡下，所以这里用一个不可能连上的地址也无妨
	if _, err := NewUploader("http://127.0.0.1:1").Upload(context.Background(), nil); err == nil {
		t.Fatal("空 data item 应当被拒绝")
	}
}

func TestUploadReportsMissingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if _, err := NewUploader(srv.URL).Upload(context.Background(), []byte("x")); err == nil {
		t.Fatal("响应缺 id 时应当报错")
	}
}

func TestDefaultEndpointUsedWhenBlank(t *testing.T) {
	u := NewUploader("   ")
	if u.Endpoint != DefaultEndpoint {
		t.Fatalf("endpoint = %q", u.Endpoint)
	}
}

// 429 与 5xx 应当退避重试，而不是直接放弃。
func TestUploadRetriesOnRetryableStatus(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"after-retry"}`))
	}))
	defer srv.Close()

	u := NewUploader(srv.URL)
	u.RetryBackoff = time.Millisecond

	id, err := u.Upload(context.Background(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "after-retry" {
		t.Fatalf("id = %q", id)
	}
}

// 400 这类重试无意义的状态码应当立刻失败，不做无谓重试。
func TestUploadDoesNotRetryClientError(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		http.Error(w, "bad data item", http.StatusBadRequest)
	}))
	defer srv.Close()

	u := NewUploader(srv.URL)
	u.RetryBackoff = time.Millisecond

	if _, err := u.Upload(context.Background(), []byte("x")); err == nil {
		t.Fatal("400 应当报错")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("400 不该重试，实际请求 %d 次", attempts)
	}
}
