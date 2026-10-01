package publish

import (
	"path/filepath"
	"strings"
	"testing"
)

// 同一个站点与目标，同时只能有一条发布在跑。
func TestAcquireRejectsSecond(t *testing.T) {
	root := t.TempDir()

	release, err := Acquire(root, "arweave")
	if err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(root, "arweave")
	if err == nil {
		t.Fatal("第二次应当被拒")
	}
	if !strings.Contains(err.Error(), "在跑") {
		t.Fatalf("错误信息应当说清原因: %v", err)
	}

	release()

	// 释放之后应当能再拿
	again, err := Acquire(root, "arweave")
	if err != nil {
		t.Fatalf("释放后应当能再拿: %v", err)
	}
	again()
}

// 不同目标、不同站点互不影响。
func TestAcquireIsolatesBySiteAndTarget(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()

	ra, err := Acquire(a, "arweave")
	if err != nil {
		t.Fatal(err)
	}
	defer ra()

	// 同一站点换目标
	rb, err := Acquire(a, "local")
	if err != nil {
		t.Fatalf("不同目标不该互相挡: %v", err)
	}
	rb()

	// 换个站点
	rc, err := Acquire(b, "arweave")
	if err != nil {
		t.Fatalf("不同站点不该互相挡: %v", err)
	}
	rc()
}

// 释放函数重复调用不该出问题，也不该顺手把别人的名额放掉。
func TestReleaseIsIdempotent(t *testing.T) {
	root := t.TempDir()

	release, err := Acquire(root, "arweave")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // 再来一次

	second, err := Acquire(root, "arweave")
	if err != nil {
		t.Fatalf("释放后应当能再拿: %v", err)
	}
	second()
}

// 路径写法不同但指向同一目录时，应当算同一个名额。
//
// Windows 上大小写也不敏感，一并归一。
func TestAcquireNormalizesPath(t *testing.T) {
	root := t.TempDir()

	release, err := Acquire(root, "arweave")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := Acquire(root+string(filepath.Separator), "arweave"); err == nil {
		t.Fatal("同一目录的不同写法应当算同一个名额")
	}
	if _, err := Acquire(strings.ToUpper(root), "arweave"); err == nil {
		t.Fatal("大小写不同但同一目录，应当算同一个名额")
	}
}
