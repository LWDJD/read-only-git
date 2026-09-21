package signer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
)

// 从服务里取出下一个待签任务，模拟签名页的行为。
func nextTask(t *testing.T, svc *Service) Request {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := http.Get(svc.URL() + "api/next")
		if err != nil {
			t.Fatal(err)
		}
		var task Request
		decodeErr := json.NewDecoder(res.Body).Decode(&task)
		res.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if task.ID != "" {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatal("等不到待签任务")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func postSignature(t *testing.T, svc *Service, id, body string) {
	t.Helper()
	res, err := http.Post(svc.URL()+"api/sign/"+id, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("回传签名应当返回 204，实际 %d", res.StatusCode)
	}
}

// 交易类任务：页面拿到的 kind 是 tx，回传的是字段而非字节。
func TestSignTxReturnsParsedFields(t *testing.T) {
	svc := New([]byte("<html></html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got := make(chan *arweave.TxSignature, 1)
	failed := make(chan error, 1)
	tags := []arweave.Tag{{Name: "Bundle-Format", Value: "binary"}}

	go func() {
		sig, err := svc.SignTx(ctx, []byte("bundle-bytes"), tags)
		if err != nil {
			failed <- err
			return
		}
		got <- sig
	}()

	task := nextTask(t, svc)
	if task.Kind != KindTx {
		t.Fatalf("交易任务的 kind 应为 %q，实际 %q", KindTx, task.Kind)
	}
	if len(task.Tags) != 1 || task.Tags[0].Name != "Bundle-Format" {
		t.Fatalf("标签应当原样带过去: %+v", task.Tags)
	}

	postSignature(t, svc, task.ID,
		`{"id":"tx-1","owner":"owner-1","signature":"sig-1","reward":"100","last_tx":"anchor","data_root":"root-1"}`)

	select {
	case sig := <-got:
		if sig.ID != "tx-1" || sig.Owner != "owner-1" || sig.Signature != "sig-1" {
			t.Fatalf("签名字段没解析对: %+v", sig)
		}
		if sig.Reward != "100" || sig.LastTx != "anchor" {
			t.Fatalf("reward 与 last_tx 也要带上: %+v", sig)
		}
		if sig.DataRoot != "root-1" {
			t.Fatalf("data_root 也要带上: %+v", sig)
		}
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("等不到签名结果")
	}
}

// 内容类任务的 kind 是 dataitem，保持原有行为。
func TestSignDataItemCarriesKind(t *testing.T) {
	svc := New([]byte("<html></html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan []byte, 1)
	go func() {
		signed, err := svc.Sign(ctx, []byte("content"), nil)
		if err == nil {
			done <- signed
		}
	}()

	task := nextTask(t, svc)
	if task.Kind != KindDataItem {
		t.Fatalf("内容任务的 kind 应为 %q，实际 %q", KindDataItem, task.Kind)
	}

	postSignature(t, svc, task.ID, "signed-bytes")

	select {
	case signed := <-done:
		if string(signed) != "signed-bytes" {
			t.Fatalf("应当原样回传字节，实际 %q", signed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等不到签名结果")
	}
}

// 页面回传的字段不完整时要报错，不能把半个交易放过去。
func TestSignTxRejectsIncompleteFields(t *testing.T) {
	svc := New([]byte("<html></html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	failed := make(chan error, 1)
	go func() {
		_, err := svc.SignTx(ctx, []byte("bundle"), nil)
		failed <- err
	}()

	task := nextTask(t, svc)
	// 少了 signature
	postSignature(t, svc, task.ID, `{"id":"tx-1","owner":"owner-1"}`)

	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("字段不全时应当报错")
		}
		if !strings.Contains(err.Error(), "不完整") {
			t.Fatalf("错误信息应点明字段不完整: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("字段不全时不该一直等下去")
	}
}

// 页面回传的不是合法 JSON 时也要报错。
func TestSignTxRejectsUnparsableBody(t *testing.T) {
	svc := New([]byte("<html></html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	failed := make(chan error, 1)
	go func() {
		_, err := svc.SignTx(ctx, []byte("bundle"), nil)
		failed <- err
	}()

	task := nextTask(t, svc)
	postSignature(t, svc, task.ID, "this is not json")

	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("非法 JSON 应当报错")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("非法 JSON 时不该一直等下去")
	}
}

// 内嵌资源要能取到，否则页面加载不了 arweave-js。
func TestVendorAssetsAreServed(t *testing.T) {
	svc := New([]byte("<html></html>"))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	for _, path := range []string{"vendor/arweave.js", "vendor/arweave-LICENSE.txt"} {
		res, err := http.Get(svc.URL() + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s 应当可取，实际 %d", path, res.StatusCode)
		}
	}

	if len(arweaveBundle) == 0 {
		t.Fatal("arweave 构建没被嵌进来")
	}
	if !strings.Contains(string(arweaveLicense), "MIT") {
		t.Fatal("许可证原文应当一并内嵌")
	}
}
