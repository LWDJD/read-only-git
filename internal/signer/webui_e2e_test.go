package signer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
)

// TestWebUIEndToEnd 是给人/浏览器跑的自测入口，默认跳过。
//
// 设 WUI_E2E=1 后它会起服务并一直等，直到外部把签名送回来——用来端到端
// 验证签名页与钱包交互那一层。常规 `go test ./...` 不会碰到它。
func TestWebUIEndToEnd(t *testing.T) {
	if os.Getenv("WUI_E2E") != "1" {
		t.Skip("设 WUI_E2E=1 才会跑（会阻塞等待外部签名）")
	}

	svc := New([]byte(DefaultPage))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// 直接打到 stdout，方便外部抓取
	fmt.Println("PAGE_URL=" + svc.URL())

	tasks := []struct{ path, body string }{
		{"index.html", "<h1>demo</h1>"},
		{"src/app.js", "console.log(1)"},
		{"a.txt", "hello world"},
	}

	for i, task := range tasks {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		signed, err := svc.Sign(ctx, []byte(task.body), []arweave.Tag{
			{Name: "Path", Value: task.path},
			{Name: "App-Name", Value: "read-only-git"},
		})
		cancel()
		if err != nil {
			t.Fatalf("第 %d 个任务签名失败: %v", i+1, err)
		}
		if len(signed) == 0 {
			t.Fatalf("第 %d 个任务返回了空签名", i+1)
		}
		fmt.Printf("SIGNED %d/%d %s (%d bytes)\n", i+1, len(tasks), task.path, len(signed))
	}

	fmt.Println("ALL_DONE")
}
