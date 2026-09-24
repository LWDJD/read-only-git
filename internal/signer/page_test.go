package signer

import (
	"strings"
	"testing"
)

// 签名页不该自己改 reward：价格交给 arweave-js 去问节点的 /price。
//
// 这里曾经乘过 2（排查「交易上了链、网关却打不开」时为了排除变量加的），
// 真因最后落在 bundle 缺少 ANS-104 头部上，与手续费无关。
// 这条测试是防止它被再加回来：一旦有人在页面里动 reward，这里当场报。
//
// 只禁「赋值」不禁「读取」：页面要把 reward 回传给 Go 写日志。
func TestSignerPageDoesNotOverrideReward(t *testing.T) {
	for _, bad := range []string{"REWARD_MULTIPLIER", "bumpReward", "tx.reward =", "tx.reward="} {
		if strings.Contains(DefaultPage, bad) {
			t.Errorf("签名页里不该出现 %q：reward 应当照节点报价，不额外加价", bad)
		}
	}
	if !strings.Contains(DefaultPage, "createTransaction") {
		t.Error("签名页应当用 arweave-js 的 createTransaction 构造交易")
	}
}

// bundle 的签名与提交都在这一页完成，两处 js 文件都要保留这个形状。
func TestSignerPageSignsAndUploads(t *testing.T) {
	for _, want := range []string{"transactions.sign", "transactions.verify", "signAndUploadBundle"} {
		if !strings.Contains(DefaultPage, want) {
			t.Errorf("签名页里应当有 %q", want)
		}
	}
}

// 多块时签名页也不提交，只回传 proofs，由 Go 提交。
//
// 理由同 webui：arweave-js 的 upload() 不暴露响应体，
// 它说成功时你没法知道节点实际回了什么。
func TestSignerPageHandsMultiChunkToGo(t *testing.T) {
	for _, good := range []string{"uploaded: false", "proofs: proofs", "data_path"} {
		if !strings.Contains(DefaultPage, good) {
			t.Errorf("签名页里应当有 %q", good)
		}
	}
	if strings.Contains(DefaultPage, "transactions.upload") {
		t.Error("签名页不该再用 transactions.upload：它不暴露响应体")
	}
}
