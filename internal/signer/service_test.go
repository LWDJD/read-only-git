package signer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
)

// playPage 模拟页面的行为：轮询任务、取内容、回传「签名」。
func playPage(ctx context.Context, t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := client.Get(base + "api/next")
		if err != nil {
			t.Error(err)
			return
		}
		var task Request
		decodeErr := json.NewDecoder(res.Body).Decode(&task)
		res.Body.Close()
		if decodeErr != nil {
			t.Error(decodeErr)
			return
		}

		if task.ID == "" {
			// 任务还没排进来，稍后再看
			time.Sleep(10 * time.Millisecond)
			continue
		}

		blobRes, err := client.Get(base + "api/blob/" + task.ID)
		if err != nil {
			t.Error(err)
			return
		}
		data, _ := io.ReadAll(blobRes.Body)
		blobRes.Body.Close()

		signed := append([]byte("signed:"), data...)
		signRes, err := client.Post(base+"api/sign/"+task.ID,
			"application/octet-stream", bytes.NewReader(signed))
		if err != nil {
			t.Error(err)
			return
		}
		signRes.Body.Close()
	}
}

func TestSignRoundTrip(t *testing.T) {
	svc := New([]byte("<html>page</html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go playPage(ctx, t, svc.URL())

	signed, err := svc.Sign(ctx, []byte("payload"), []arweave.Tag{{Name: "Path", Value: "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(signed) != "signed:payload" {
		t.Fatalf("signed = %q", signed)
	}
}

func TestSignMultipleSequentially(t *testing.T) {
	svc := New(nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go playPage(ctx, t, svc.URL())

	for _, want := range []string{"one", "two", "three"} {
		signed, err := svc.Sign(ctx, []byte(want), nil)
		if err != nil {
			t.Fatalf("签名 %q 失败: %v", want, err)
		}
		if string(signed) != "signed:"+want {
			t.Fatalf("signed = %q", signed)
		}
	}
}

func TestServesPageOnLoopbackOnly(t *testing.T) {
	svc := New([]byte("<html>hello-page</html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	if !strings.HasPrefix(svc.URL(), "http://127.0.0.1:") {
		t.Fatalf("应当只绑回环地址，实际 %q", svc.URL())
	}

	res, err := http.Get(svc.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "hello-page") {
		t.Fatalf("页面内容不对: %q", body)
	}
}

func TestNextReportsEmptyWhenIdle(t *testing.T) {
	svc := New(nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	res, err := http.Get(svc.URL() + "api/next")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var task Request
	if err := json.NewDecoder(res.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "" {
		t.Fatalf("空闲时应返回空 id，实际 %q", task.ID)
	}
}

func TestSignHonorsContextCancel(t *testing.T) {
	svc := New(nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	if _, err := svc.Sign(ctx, []byte("x"), nil); err == nil {
		t.Fatal("ctx 取消后应返回错误，而不是一直等")
	}
}

func TestPendingCountsUnfinished(t *testing.T) {
	svc := New(nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	if got := svc.Pending(); got != 0 {
		t.Fatalf("空闲时 Pending = %d", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go playPage(ctx, t, svc.URL())

	if _, err := svc.Sign(ctx, []byte("x"), nil); err != nil {
		t.Fatal(err)
	}
	if got := svc.Pending(); got != 0 {
		t.Fatalf("签完后 Pending = %d", got)
	}
}

func TestUnknownTaskReturnsNotFound(t *testing.T) {
	svc := New(nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	res, err := http.Get(svc.URL() + "api/blob/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("未知任务应 404，实际 %d", res.StatusCode)
	}
}
