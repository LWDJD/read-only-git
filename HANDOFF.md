# 交接笔记

给下一个上下文窗口的自己看。当前状态、关键决策、踩过的坑，都在这里。

## 这是什么

read-only-git 分两半：

| | 给谁 | 是什么 |
|---|---|---|
| 静态站（`public/`） | 任何人 | 部署到 Arweave 等静态托管的产物。访客能 `git clone`，也能在网页里浏览文件树和历史 |
| 维护器（`rog`，Go） | 只有作者 | 把 git 仓库打包成静态站产物，并发布出去 |

维护器要 `go build` 出自己的副本；静态站那边零依赖、零安装。

## 当前状态（2026-09-21）

分支 `dev`，最新提交 `1f50cd5`。

**已完成**

- `internal/repopack` 打包裸仓库，全量 + 增量，产出符合 dumb HTTP 契约
- `internal/publish` 发布目标抽象 + 本地目录实现
- `internal/arweave` 上传 data item、tags、manifest、增量复用
- `internal/signer` 本机签名服务（只绑 127.0.0.1）+ 内置签名页，接浏览器钱包
- `main.go` `rog pack` / `rog publish`（本地 / `--arweave`）
- 前端：ref 下拉、面包屑保持 ref

**未完成**

- ENS contenthash 写回（目前手动在 ENS app 填 `ar://<入口id>`）
- 真实 Arweave 端点 + 真实钱包的端到端验证（需要作者参与）
- README 重写（还停留在 Python 脚本时代）
- 旧脚本去留：`scripts/` 下的 Python / Node 与 Go 版并存
- `internal/signer/e2e/main.go` 是开发探针，去留待定
- 前端还没有 release 展示、没有 diff 展示（作者说优先级低）

## 命令

```bash
rog pack [--update] <源仓库> [站点根] [仓库名]
rog publish <站点根> <本地目录>
rog publish <站点根> --arweave [--repo 名字] [--endpoint 地址]
rog help
```

## 硬约束（血泪换来的，改代码前必读）

1. **pack 文件名必须是 `pack-<40位hex>.pack`**。git 解析 `objects/info/packs` 时严格匹配，
   格式不对的行被**静默跳过**，最后只报 `Unable to find <oid>`，看不出真正原因。
2. **`refs/` 目录即使空了也必须保留**。删掉后 git 直接报 `not a git repository`。
   `refs/heads/`、`refs/tags/` 两个空子目录也是标准布局，别删。
3. dumb 客户端**不请求** `config` / `packed-refs` / `description`；
   `objects/info/http-alternates` 与 `alternates` 会各被请求一次拿 404，属正常流程。
4. 操作「产物目录」的 git 命令**必须用 `--git-dir` 明确指定**。用 `-C`/工作目录时，
   git 会向上发现父仓库，产物一旦嵌在别的仓库里就会读到父仓库的 refs。
5. **增量必须保留旧 pack 不动**（不 `repack`、不 `gc`），否则内容寻址存储上旧数据无法复用，
   每次更新都要重付一遍钱。
6. **增量时「传对象」与「对齐 ref」的范围必须一致**，都是 `refs/heads` + `refs/tags`。
   范围不一致会导致两个方向的 bug：新 ref 被丢弃、或对齐到不存在的对象而永久失败。
7. **`.panel` 不能用 `overflow: hidden` 修圆角**，会剪掉内部浮层（ref 下拉），
   且被剪掉的部分点不到。改由首尾子元素自己贴圆角。
8. **CSS 里给元素写 `display` 会覆盖 `[hidden]` 的 `display: none`**（UA 样式优先级最低），
   结果就是元素恒显、属性形同虚设。需要补 `[hidden] { display: none }`。

## 环境相关的坑（当前沙箱）

- `git clone --bare <本地路径>` **必然失败**（`sh.exe` 建不出信号管道），
  打包总是走 bundle 回退链路。两条链路都要能跑。
- `git fetch <本地路径>` 同样失败，所以增量改用 bundle + `--not`。
- Node 的 `child_process` 无法 spawn 任何进程（EPERM），
  所以 `selftest.mjs` 的 fixture 模式跑不了，要用 `node scripts/selftest.mjs <裸仓库>`。
- Go 的网络可用（`upload.ardrive.io`、`turbo.ardrive.io` 都能连）。
- git 的写操作（`switch` / `add` / `commit`）时通时不通，可能被权限拦下。
- `GOMODCACHE` 指向别的项目会导致 `go build` 报 "writing stat cache" 警告，无害。

## 测试

`go test ./...` 全绿，67 个用例：repopack 23 / publish 13 / arweave 23 / signer 8。

另外两条：

```bash
node scripts/selftest.mjs <裸仓库>     # 前端解析器（JS 侧）
WUI_E2E=1 go test -run TestWebUIEndToEnd ./internal/signer/
                                        # 起真服务等浏览器操作，人工验签名页
```

## 外部测试循环

有一个独立的测试 agent 在跑对抗测试，报告在
`D:\Project\openhanako\test\read-only-git\_adv*\REPORT_v*.md`，已到第十轮。
它找出的问题基本都修完了，这个循环值得继续走。

## 前端约定

- 零依赖、零构建，直接改 `public/` 下的文件
- 视觉对齐 GitHub：1px 细边框、6px 圆角、配色取 GitHub 实际色值
- `views.js` 用 `h()` 构造 DOM，没有框架
- **改完前端要整页重载再验证**：站点会把仓库数据缓存在内存里，
  只改 hash 不重新加载（可以加个 `?v=N` 强制刷新）

## 给自己的教训

1. **有界面的东西，验收标准必须包含「界面最终呈现什么」**，不是「后端日志里有什么」。
   属性和渲染是两回事，要看 `getComputedStyle` / 实际高度。
2. **别只看中间态**，跑完再判断。
3. 「跑通了」不等于「跑对了」。
