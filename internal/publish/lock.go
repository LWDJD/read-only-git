package publish

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// 进程内的发布互斥。
//
// 管的是「同一个站点、同一个目标」被同时发布两次的情况：
// 界面上连点两下按钮、或者同一进程里并发发起，都可能撞上。
// 两条发布同时写同一份记录、抢同一批待签名内容，结果既难预料也难解释。
//
// 跨进程的那一层不在这里：那是各存储自己的事（repopack 已有文件锁）。
var (
	activeMu sync.Mutex
	active   = map[string]bool{}
)

// Acquire 尝试占住一个发布名额。
//
// 返回的释放函数必须调用，通常 defer 掉。同一个站点与目标已有发布在跑时
// 返回错误，调用方应当把话说给用户听，而不是排队等下去：
// 排队会让「界面卡住」和「服务在干活」看起来一模一样。
func Acquire(siteRoot, target string) (func(), error) {
	key := lockKey(siteRoot, target)

	activeMu.Lock()
	defer activeMu.Unlock()

	if active[key] {
		return nil, fmt.Errorf("%s 上已有一个发往 %s 的发布在跑，等它结束再试",
			siteRoot, target)
	}
	active[key] = true

	var once sync.Once
	return func() {
		once.Do(func() {
			activeMu.Lock()
			delete(active, key)
			activeMu.Unlock()
		})
	}, nil
}

// lockKey 把站点与目标拼成一个键。
//
// 用 \x00 分隔，免得路径尾部与目标名粘在一起产生歧义。
// 路径统一小写：Windows 上大小写不敏感，不这样同一目录会被当成两个。
func lockKey(siteRoot, target string) string {
	return strings.ToLower(filepath.Clean(siteRoot)) + "\x00" + target
}
