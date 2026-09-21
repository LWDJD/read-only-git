// 命令 wuitest 是开发期的端到端探针：起一个真实的签名服务、注册若干待签名
// 任务，等浏览器里的页面把结果送回来。用来验证签名页与钱包交互那一层。
//
// 它不在正式交付路径上，只服务于本地自测。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
	"github.com/LWDJD/read-only-git/internal/signer"
)

func main() {
	svc := signer.New([]byte(signer.DefaultPage))
	if err := svc.Start(); err != nil {
		fmt.Println("start failed:", err)
		os.Exit(1)
	}

	fmt.Println("PAGE_URL=" + svc.URL())

	payloads := []struct {
		path string
		body string
	}{
		{"index.html", "<h1>demo</h1>"},
		{"src/app.js", "console.log(1)"},
		{"a.txt", "hello world"},
	}

	for i, p := range payloads {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		signed, err := svc.Sign(ctx, []byte(p.body), []arweave.Tag{
			{Name: "Path", Value: p.path},
			{Name: "App-Name", Value: "read-only-git"},
		})
		cancel()
		if err != nil {
			fmt.Printf("FAILED at %d (%s): %v\n", i+1, p.path, err)
			os.Exit(1)
		}
		fmt.Printf("SIGNED %d/%d %s -> %d bytes\n", i+1, len(payloads), p.path, len(signed))
	}

	fmt.Println("ALL_DONE")
	_ = svc.Close()
}
