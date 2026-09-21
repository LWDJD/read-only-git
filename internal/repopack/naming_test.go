package repopack

import (
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"p2ping", "p2ping"},
		{"p2ping.git", "p2ping"},
		{"p2ping.git.git", "p2ping"},
		{"  spaced  ", "spaced"},
		{"P2PING.GIT", "P2PING"},
		{".git", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsRemote(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/LWDJD/p2ping.git", true},
		{"http://host/x", true},
		{"git://host/x", true},
		{"ssh://git@host/x", true},
		{"file:///tmp/x", true},
		{"git@github.com:LWDJD/p2ping.git", true},
		// 本地路径不能被误判成 scp 风格（没有 @）
		{`D:\Project\web\read-only-git`, false},
		{`C:\Users\x\repo`, false},
		{"/home/user/repo", false},
		{"./local", false},
		{"local-repo", false},
	}
	for _, c := range cases {
		if got := IsRemote(c.in); got != c.want {
			t.Errorf("IsRemote(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRemoteName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/LWDJD/p2ping.git", "p2ping"},
		{"https://github.com/LWDJD/p2ping", "p2ping"},
		{"https://host/a/b/", "b"},
		{"git@github.com:LWDJD/p2ping.git", "p2ping"},
		{"ssh://git@host:22/team/repo.git", "repo"},
	}
	for _, c := range cases {
		if got := RemoteName(c.in); got != c.want {
			t.Errorf("RemoteName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"p2ping", true},
		{"a-b_c.d", true},
		{"console", true},
		{"com0", true},
		{"", false},
		{".", false},
		{"..", false},
		{"a/b", false},
		{`a\b`, false},
		// 跨平台不安全字符一律拒绝，避免留给文件系统报原始错误
		{"a:b", false},
		{"a|b", false},
		{"a<b", false},
		{"a?b", false},
		{"a*b", false},
		{"a\"b", false},
		{"a\x00b", false},
		{"a\tb", false},
		{"CON", false},
		{"con.txt", false},
		{"NUL", false},
		{"LPT9", false},
		// 长度上限要算上 .git 后缀，并按字符而非字节判断
		{strings.Repeat("a", 251), true},  // 251+4 = 255，刚好
		{strings.Repeat("a", 252), false}, // 256，超
		{strings.Repeat("中", 80), true},   // 244 字节，ext4 也放得下
		{strings.Repeat("中", 100), false}, // 304 字节，超 ext4 的 255 字节
	}
	for _, c := range cases {
		if got := isValidName(c.in); got != c.want {
			t.Errorf("isValidName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// fuzz 曾抽出反例 "repo .git"：先剥后缀会留下尾随空格。
func TestNormalizeNameIsIdempotent(t *testing.T) {
	cases := []string{
		"repo .git",
		"  x.git  ",
		"a.git.git",
		".git",
		"p2ping",
		"x .git .git",
	}
	for _, in := range cases {
		once := NormalizeName(in)
		twice := NormalizeName(once)
		if once != twice {
			t.Errorf("NormalizeName 不幂等: %q -> %q -> %q", in, once, twice)
		}
	}
}
