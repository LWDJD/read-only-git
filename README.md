# read-only-git

把裸仓库丢到静态托管上，访客就能 `git clone`，同时有一个网页界面浏览文件树、README 和提交历史。

站点本身零运行时依赖、零构建步骤，打开 `index.html` 就能跑。打包与发布由一个 Go 维护器 `rog` 负责，只在你自己机器上跑。

## 原理

Git 有个降级协议叫 dumb HTTP protocol，服务端只需要按约定提供几个静态文件：

```
HEAD
info/refs              所有分支和标签
objects/info/packs     pack 清单
objects/pack/*.pack
objects/pack/*.idx
```

客户端尝试 smart 协议失败后会自动降级，直接读这些文件。所以一台只会发文件的服务器就够了：Arweave 网关、IPFS 网关、Cloudflare Pages、EdgeOne、nginx 都行。

浏览界面同理，服务端不参与，在浏览器里直接解析 packfile，实现见 `public/src/git/`。

## 站点结构

```
/
├── index.html          界面
├── repository.json     仓库清单
├── src/                界面代码与样式
└── p2ping.git/         裸仓库，一个项目一个
```

`repository.json` 存在的原因：静态托管没有目录列表 API，无从得知站上放了哪些仓库。

```json
{
  "repositories": [
    { "name": "p2ping", "description": "可选的一句话说明" }
  ]
}
```

命名约定：`.git` 是目录后缀，不是仓库名的一部分。

| 位置 | 形式 | 例子 |
|---|---|---|
| 磁盘目录 | 带后缀 | `p2ping.git/` |
| `repository.json` / URL / 界面显示 | 裸名 | `p2ping` |
| clone 地址 | 带后缀 | `<站点>/p2ping.git` |

前端对 JSON 里的两种写法都做了归一化，但请按裸名写。空、`.`、`..`、含路径分隔符的条目会被忽略。

## 用法

### 打包

```
rog pack [--update] <源仓库> [输出目录] [仓库名]
```

| 参数 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `<源仓库>` | 是 | | 本地路径（普通仓库或裸仓库），或远端地址 |
| `[输出目录]` | 否 | `public` | 站点根目录，也就是放着 `index.html` 的那个 |
| `[仓库名]` | 否 | 本地取目录名，远端取地址末段 | 对外标识，带不带 `.git` 后缀等价 |
| `--update` | 否 | 关 | 目标已存在时做增量更新，保留旧 pack |

```bash
# 本地仓库
rog pack ../p2ping

# 远端仓库，直接复刻一份静态版本
rog pack https://github.com/LWDJD/p2ping.git

# 指定输出目录和名字
rog pack git@github.com:LWDJD/p2ping.git site p2ping

# 已有站点上增量更新，只处理变化的对象
rog pack --update ../p2ping site p2ping
```

尖括号是必填，方括号是可省略。

#### 远端地址

支持 `https://` `git://` `ssh://` `file://`，以及 scp 风格的 `user@host:path`。

远端模式下**拿到的是那个仓库的全部分支和标签**，不只是你本地 checkout 过的那几个。所以想给一个远端仓库做静态镜像，一条命令就够。

私有仓库得先让 git 自己拿到凭据（credential helper 或 SSH key），程序不处理认证。远端不可达或凭据不对时，git 的报错会原样带出来。

#### 输出目录要指向站点根

`pack` 只生成裸仓库和 `repository.json`，不碰前端。所以输出目录得是**放着 `index.html` 的那个目录**，否则产物没法直接部署：

```
你的项目/
  public/                 ← 站点根
    index.html            ┐ 前端，仓库还没下载时就得有
    src/                  ┘
    p2ping.git/           ← pack 写进来的
    repository.json       ← pack 维护的
```

往空目录里跑也能生成，但会在结尾提醒你还缺 `index.html`。

#### 它做的事

裸仓库输出到 `<输出目录>/<仓库名>.git/` → `repack -a -d` 把对象收进单 pack → `gc --prune=now` → `update-server-info` 生成索引 → 清掉协议用不到的文件 → 校验并打印清单 → 更新 `repository.json`。

重复执行安全：同名仓库覆盖重建，`repository.json` 里那条的 `description` 会保留。增量模式下旧 pack 保持不动，只把新对象追加进去，refs 对齐到源仓库。

产物就七个文件：

```
config  HEAD  packed-refs  info/refs  objects/info/packs
objects/pack/pack-*.idx   objects/pack/pack-*.pack
```

<details>
<summary>手工命令</summary>

```bash
git clone --bare <你的仓库> public/p2ping.git
git -C public/p2ping.git repack -a -d
git -C public/p2ping.git gc --prune=now
git -C public/p2ping.git update-server-info
```

`repack` 不能省。对象要是散着的，客户端得逐个请求，一个中等项目就是上千次 HTTP 请求。

</details>

<details>
<summary>clone --bare 失败时的回退链路</summary>

在沙箱或受限的 `sh.exe` 环境下，本地传输建不出管道，`clone --bare` 会报 `couldn't create signal pipe`。程序这时自动改用 bundle：

```
git bundle create <bundle> --branches --tags   （在源仓库）
git init --bare <target>
git bundle unbundle <bundle>                  （列出 refs 到 stdout）
git update-ref <ref> <sha>                    （逐个建 ref，必须做）
git symbolic-ref HEAD <ref>
```

`update-ref` 这步不能省：`bundle unbundle` 只把 refs 列出来，不创建它们。refs 不存在的话，后面 `gc --prune=now` 会把所有对象当不可达清掉，产出一个空仓库。

用 `--branches --tags` 而不是 `--all`，后者会把 `refs/remotes` 一并带进来。

</details>

### 发布

发布要签名，私钥在浏览器钱包里，所以会起一个只绑本机的小服务，让你在页面上签字。装 [Wander](https://wander.app/) 即可。

三条路：

| 目标 | 命令 | 说明 |
|---|---|---|
| 本地目录 | `rog publish <站点> <目标目录>` | 试跑用，只拷文件，不花钱 |
| Turbo | `rog publish <站点> --arweave` | 默认。逐个把文件交给上传服务 |
| L1 | `rog publish <站点> --arweave --l1` | 打成一包，签一笔交易直接提交到节点，不经过任何服务 |

```bash
# 先本地试一遍，确认站点结构没问题
rog publish . /tmp/preview

# 发布到 Turbo
rog publish . --arweave --repo p2ping

# 走 L1，直接提交到 arweave.net
rog publish . --arweave --l1 --repo p2ping
```

跑起来会打印一个签名页地址，浏览器里连接钱包、点「开始签名」，签完自动继续。

**增量**：第二次发布只处理内容变了的文件，其余复用上次的 id，不为已付费的内容再付一次。记录存在 `<站点>/.rog/` 下。

**体积**：L1 会按 256 KiB 自动分块，多大的站点都能发。分块协议要求把交易先报上去、再逐块补内容，这些程序自己处理。

**费用**：Arweave 是永久存储，按体积一次性付费。Turbo 有免费额度（约单项 105 KiB、终身 10 MiB），小项目通常够用，超出部分要充值。具体额度以官方页面为准。L1 不经服务，直接付给网络。

### 从链上恢复

```
rog publish <站点> --arweave --from <入口id> [--repo <名字>] [--gateway <地址>]
```

发布记录本身也会随站点上链。换机器、本地 `.rog` 丢了之后，用入口 id 把它取回来，增量能力就续上了，不必从零重传。

`--gateway` 用来指定读取用的网关，默认 `arweave.net`。

### 图形界面

```
rog webui [--site <站点目录>] [--port <端口>]
```

| 选项 | 默认 | 说明 |
|---|---|---|
| `--site <目录>` | `public` | 默认操作的站点目录 |
| `--port <端口>` | 随机空闲端口 | 固定端口，方便反复访问同一个地址 |

把打包、发布、从链上恢复、站点文件浏览与替换都摆在页面上，功能与命令行一致。耗时操作会开一个任务，进度用 SSE 实时推，刷新页面也不丢。

服务只绑 `127.0.0.1`，并且启动时生成一个随机会话 token：地址栏里带着它，页面内所有请求也跟着带，不带或不对一律拒绝。这样即使同机有别的网页，也没法借你的浏览器去改站点文件。

## 界面

仓库页是两栏结构，左栏文件树和文档，右栏 About 和 Releases。

- **About**：简介取 `repository.json` 的 `description`，没写就退回最新提交标题；许可证扫根目录的 `LICENSE` / `COPYING` / `UNLICENSE`，按内容特征串识别类型（MIT、Apache-2.0、GPL 系、BSD 系、ISC、MPL、Unlicense 等），认不出就显示文件名
- **Releases**：没有 release 概念，用标签近似，按版本号排序，最高的标 `Latest`
- **文档区**：文件列表下方的 tab，识别 `readme` / `license` / `changelog` / `contributing` / `code_of_conduct` / `security` / `governance` / `citation` / `authors` / `support`，默认打开 README。`.md` 走 Markdown 渲染，其余（License 之类）用等宽文本保留原始排版。子目录页同样适用

标签排序用 `localeCompare` 带 `numeric: true`，否则 `v10.0.0` 会排在 `v9.0.0` 前面。

### 路由

静态托管没有服务端重写能力，路由只能用 hash。hash 部分不发给服务器，深链接随便用。

```
#/                        项目列表
#/p2ping                  概览
#/p2ping/tree/main/src    目录
#/p2ping/blob/main/x.rs   文件
#/p2ping/commits/main     提交历史
#/p2ping/branches         分支
#/p2ping/tags             标签
```

### 换肤

可调项都收在 `public/src/style.css` 顶部的 CSS 变量里：

```css
:root {
  --bg:          #ffffff;
  --bg-subtle:   #f6f8fa;
  --text:        #1f2328;
  --text-muted:  #59636e;
  --border:      #d1d9e0;
  --accent:      #0969da;
  --accent-mark: #fd8c73;   /* 选中 tab 的下划线 */
  --page-width:  1280px;
  --side-width:  296px;
}
```

深色模式跟随系统，也可以用 `<html data-theme="dark">` 强制。改结构或文案动 `public/src/views.js`。

## 技术说明

### 解压边界

`DecompressionStream` 对尾部多余字节零容忍，会抛 `Trailing junk found after the end of the compressed stream`，而 packfile 里每个对象的 zlib 流没有长度字段。

解法是用 pack index 的偏移表反推边界：所有对象偏移排序后，某个对象的字节区间就是 `[自身偏移, 下一个偏移)`，切出来刚好是完整压缩数据。所以 `.idx` 是必需件。

### 分块的切法从哪来

L1 发布大站点时要算 Merkle 树，而交易签名绑定的 `data_root` 就来自那棵树。

这里的做法是：`data_root` 与各块的 proof 都交给签名页里的 arweave-js 算，Go 侧只按回传的 `offset` 推每块边界，再逐块提交。这样签名的依据与提交的依据天然是同一份，不存在两个实现算不到一块去的可能。

有个细节值得记：恰好等于单块上限（256 KiB）的数据只有一块要传，但它的 proof 比预期长。原因是切块时先切出一个零长度尾块、树按含它的形状建好，之后才把空块丢掉。所以自己照规则重切一遍反而容易错，直接认 offset 最稳。

`scripts/arjs-vectors.cjs` 能把 arweave-js 的输出固化成对照数据，测试里会拿它比对。

### 模块

```
public/src/git/
  zlib.js      解压封装
  idx.js       pack index v2 解析
  pack.js      pack 读取、delta 递归
  delta.js     delta 指令解码
  objects.js   commit / tree / tag 结构
  repo.js      仓库门面：refs / tree / blob / log
```

`RemoteRepo` 接受 `baseUrl` 和可注入的 `fetch`，所以能在 Node 环境跑：

```bash
node scripts/selftest.mjs                 # 自建 fixture，用完即删
node scripts/selftest.mjs <裸仓库目录>     # 或指定一个现成的
```

它会走一遍 pack 加载、refs 解析、tree 递归、blob 解压、提交历史。

## 已知限制

- **只读**。协议层面没有 push 路径，这正是它能用纯静态文件托管的原因
- **源仓库不能是浅克隆**。`git clone --depth` 拉下来的历史不完整，`repack` 遍历父提交时会失败。程序会提前拦住并提示 `fetch --unshallow`
- **整包下载**。packfile 一次性载入内存，几十 MB 会卡。要优化得解析 `.idx` 后按 Range 惰性取对象
- **不支持 shallow clone**。`--depth` 依赖服务端裁剪历史
- **不能选择性公开**。`info/refs` 列出仓库里所有 refs，不想公开的分支要在打包前清掉
- **平台可能拦 `.git` 路径**。带 WAF 的托管会挡 `/.git/` 前缀，部署后先 curl 一下 `xxx.git/info/refs` 确认。被拦就把目录改名成 `xxx.repo` 之类，clone 地址相应变化（git 不要求 URL 以 `.git` 结尾）
- **ENS contenthash 要手动填**。发完在 ENS 应用里把入口写成 `ar://<入口id>`，这一步尚未自动化

## 目录

```
main.go                    命令行入口：pack / publish / webui
internal/repopack/         打包：全量、增量、ref 对齐、文件锁
internal/publish/          发布抽象、本地目标、发布记录、进程内互斥
internal/arweave/          Turbo 与 L1 两条路：上传、bundle、分块、manifest
internal/signer/           本机签名服务与内嵌签名页（含 arweave-js）
internal/webui/            维护台：任务、接口、内嵌页面
public/                    部署根，整个上传
  index.html               入口
  repository.json          仓库清单（由 pack 维护）
  src/
    main.js                启动与路由分发
    router.js              hash 路由
    store.js               repository.json 与仓库缓存
    views.js               各视图渲染
    ui.js                  DOM 构造与小工具
    markdown.js            极简 Markdown 渲染（已做 HTML 转义）
    style.css              主题层
    git/                   只读 git 解析器
scripts/                   不参与部署
  serve.mjs                本地静态服务器
  selftest.mjs             解析器自测
  arjs-vectors.cjs         生成分块对拍的对照数据
```

## 构建

```bash
go build -o rog .
```

没有第三方依赖，用一个标准库足够。改前端不用构建：`public/` 直接就是产物。

改外观只动 `public/src/style.css`，改结构或文案动 `public/src/views.js`。
