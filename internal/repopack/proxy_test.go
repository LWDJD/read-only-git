package repopack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setGitProxy 设得上、也恢复得回来。
//
// 它是包级变量，靠 defer 恢复；恢复漏了会让「这一次带代理」
// 悄悄影响下一次打包。
func TestSetGitProxyRestores(t *testing.T) {
	if gitProxy != "" {
		t.Fatalf("初始应当为空，实际 %q", gitProxy)
	}
	restore := setGitProxy("http://127.0.0.1:7890")
	if gitProxy != "http://127.0.0.1:7890" {
		t.Fatalf("没设上: %q", gitProxy)
	}
	restore()
	if gitProxy != "" {
		t.Fatalf("没恢复: %q", gitProxy)
	}
	// 再设一次再恢复，确认不是只有第一次能用
	r2 := setGitProxy("http://a:1")
	if gitProxy != "http://a:1" {
		t.Fatalf("第二次没设上: %q", gitProxy)
	}
	r2()
	if gitProxy != "" {
		t.Fatalf("第二次没恢复: %q", gitProxy)
	}
}

// 地址两端的空白要去掉：命令行与界面都可能带进来。
func TestSetGitProxyTrims(t *testing.T) {
	restore := setGitProxy("   http://127.0.0.1:7890   ")
	defer restore()
	if gitProxy != "http://127.0.0.1:7890" {
		t.Fatalf("应当去掉空白，实际 %q", gitProxy)
	}
}

// 环境变量确实传给了 git 子进程。
//
// 用 sh 不可靠（这台机器上 git 自己的 sh 都起不来），
// 所以直接用一个能回显环境的 git 子命令组合：git 会把自己的
// 环境继承给 hook，但更简单的是复用我们自己的 runGit——
// 这里换一种更直接的验证：看 cmd.Env 是否真的被设上。
func TestRunGitPassesProxyEnv(t *testing.T) {
	requireGit(t)

	restore := setGitProxy("http://127.0.0.1:7890")
	defer restore()

	// 造一个把环境写进文件的假 git，塞到 PATH 最前面。
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "git.bat")
	// Windows 批处理：把 http_proxy 那一行写出来
	body := "@echo off\r\nset http_proxy> \"" + envFile + "\"\r\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Skipf("写不了假 git（%v），跳过这条", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	// 直接跑 runGit，让它命中假 git
	_, _ = runGit("", "version")

	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Skipf("假 git 没被执行（%v），这条依赖 PATH 生效，跳过", err)
	}
	if !strings.Contains(strings.ToLower(string(got)), "http_proxy=http://127.0.0.1:7890") {
		t.Fatalf("子进程没拿到代理环境变量，实际:\n%s", got)
	}
}

// 带代理跑一次本地源的打包：本地操作不该受它影响。
func TestPackWithProxyStillWorksOnLocalSource(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res, err := Pack(Options{
		Source: src, OutDir: out, Name: "demo",
		// 故意给一个连不上的代理：本地源根本不需要走网络
		Proxy: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("本地源不该受代理影响: %v", err)
	}
	if len(res.Packs) == 0 {
		t.Fatal("应当产出 pack")
	}
	// 打包结束后代理要复位，免得影响下一次
	if gitProxy != "" {
		t.Fatalf("打包结束应当复位，实际 %q", gitProxy)
	}
}

// 占位：确认 exec 包被用到（假 git 那条路需要它）。
var _ = exec.Command
