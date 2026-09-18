# read-only-git

把 `xxx.git` 目录直接丢到任何静态托管上，别人就能 `git clone`，同时还有一个网页界面可以浏览文件树、README 和提交历史。

**零运行时依赖，零构建步骤。** 打开 `index.html` 就能跑。

## 为什么能work

Git 有一个几乎被遗忘的降级协议，官方文档叫 **dumb HTTP protocol**：

> This protocol is called "dumb" because it requires no Git-specific code on the server side during the transport process.

它只需要服务端按约定提供几个静态文件：

```
HEAD
info/refs               所有分支和标签
objects/info/packs      pack 文件清单
objects/pack/*.pack
objects/pack/*.idx
```

客户端会自动尝试 smart 协议，发现不是之后降级到 dumb，直接用拿到的响应继续。所以一台只会发文件的服务器就够了 —— Arweave 网关、IPFS 网关、Cloudflare Pages、EdgeOne、nginx 全都行。

浏览器端的浏览界面也是同一个思路：不用 isomorphic-git（它面向 smart 协议），而是直接在浏览器里解析 packfile。为此实现了一个精简的 git 只读解析器，见 `src/git/`。

## 目录约定

整个站点只有两种东西：

```
/                        站点根
├── index.html           界面（固定不变）
├── repository.json      有哪些仓库（一个小清单）
├── src/                 界面代码与样式
└── p2ping.git/          裸仓库，一个项目一个
    ├── HEAD
    ├── info/refs
    └── objects/
```

一个仓库对应一个 `xxx.git` 目录，URL 就是 `<站点>/xxx.git`。

`repository.json` 存在的原因：静态托管没有目录列表 API。无论是 Arweave 的 path manifest、IPFS 还是 Pages，都无法回答"这个目录下有什么"。用一个小文件换跨平台通用性。

```json
{
  "repositories": [
    { "name": "p2ping", "description": "可选的一句话说明" }
  ]
}
```

### 命名规则

| 位置 | 形式 | 例子 |
|---|---|---|
| 磁盘上的目录 | 必须以 `.git` 结尾 | `p2ping.git/` |
| `repository.json` / URL / 标识 | 裸名，不含 `.git` | `p2ping` |

理由很简单：`.git` 是目录后缀，不是仓库身份的一部分。混在一起写迟早会出现 `p2ping.git.git` 这种事故。

前端对两种写法都做了归一化，JSON 里误写 `p2ping.git` 也能正常工作，但请按裸名写。

仓库名会做合法性校验：空、`.`、`..`、含 `/` 或 `\` 的条目会被忽略。

## 用法

### 1. 把仓库变成可托管的形态

```bash
python scripts/prepare-repo.py ../p2ping
python scripts/prepare-repo.py ../p2ping public p2ping
```

只依赖 Python 3 标准库加上 PATH 里的 git，不依赖 shell 方言，Windows / macOS / Linux 通用。

**参数**（按位置传）：

| 位置 | 名称 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| 1 | 源仓库 | 是 | | 普通仓库或裸仓库都行 |
| 2 | 输出目录 | 否 | `public` | |
| 3 | 仓库名 | 否 | 取源目录名 | 带不带 `.git` 等价 |

**它做的事**：

1. 把源仓库变成裸仓库输出到 `<输出目录>/<仓库名>.git/`
2. `repack -a -d` 把全部对象收进单个 pack
3. `gc --prune=now` 清掉不可达对象
4. `update-server-info` 生成 `info/refs` 和 `objects/info/packs`
5. 清掉 dumb 协议不会请求的文件（hooks 样例、reflog、index、bitmap 等）
6. 校验必需文件齐备，打印产物清单
7. 更新 `<输出目录>/repository.json`

**重复执行是安全的**：同名仓库会被覆盖重建，而 `repository.json` 里该条目的
`description` 会保留下来（不会把你写的说明冲掉）。

一个典型产物：

```
p2ping.git/config                    104 B
p2ping.git/HEAD                       23 B
p2ping.git/packed-refs               319 B
p2ping.git/info/refs                 292 B
p2ping.git/objects/info/packs         54 B
p2ping.git/objects/pack/pack-*.idx  1.3 KiB
p2ping.git/objects/pack/pack-*.pack 1.0 KiB
```

就这七个文件，没有别的。

<details>
<summary>clone --bare 在某些环境会失败</summary>

脚本首选 `git clone --bare`。在沙箱或受限的 `sh.exe` 环境下，本地传输建不出管道会报
`couldn't create signal pipe, Win32 error 5`，这时脚本会自动回退到 bundle 链路：

```
git bundle create <bundle> --branches --tags   （在源仓库）
git init --bare <target>
git bundle unbundle <bundle>                  （列出 refs）
git update-ref <ref> <sha>                    （逐个建 ref，必须做）
git symbolic-ref HEAD <ref>
```

中间那步 `update-ref` 不能省：`bundle unbundle` 只把 refs **列到 stdout**，
并不创建它们。refs 不存在的话，后面 `gc --prune=now` 会把所有对象当成不可达全部清掉，
产出一个空仓库。

另外注意用 `--branches --tags` 而不是 `--all`，后者会把 `refs/remotes` 之类也带进来。

</details>

<details>
<summary>手工执行等价命令</summary>

```bash
git clone --bare <你的仓库> public/p2ping.git
git -C public/p2ping.git repack -a -d
git -C public/p2ping.git gc --prune=now
git -C public/p2ping.git update-server-info
```

`repack` 不能省。如果对象是松散的，客户端要逐个请求，一个中等项目就是上千次 HTTP 请求。
`update-server-info` 也不能省，它生成 dumb 协议赖以工作的两个索引文件。

</details>

### 2. 本地预览

```bash
node scripts/serve.mjs public 4173
# http://localhost:4173/
```

这个服务器支持 Range 请求（dumb 协议会用它做局部下载），其余行为与真实静态托管一致。

### 3. 部署

把 `public/` 整个丢出去就行。没有任何构建产物需要生成。

- **Arweave / IPFS**：上传目录并生成 path manifest，用 manifest 的 tx id / CID 访问
- **Cloudflare Pages / EdgeOne**：直接绑定仓库或上传目录
- **nginx**：`root` 指到 `public/`

## 界面

仓库页按 GitHub 的两栏结构组织，但视觉是自己写的，没有抄它的源码。

```
┌──────────────────────────────────────────────────┐
│ ⬢ p2ping.git  [只读]                              │
│ 仓库简介                                          │
├──────────────────────────────────────────────────┤
│ <> Code | 提交 | 分支 2 | 标签 2                  │
├───────────────────────┬──────────────────────────┤
│ [master] commit 摘要   │ About                    │
│ 文件列表               │  ────                    │
│ ─────────────         │ 简介 / clone / 许可证      │
│ README 渲染            │ 统计                     │
│                       │ Releases                 │
│                       │  ────                    │
│                       │ v1.0.0  [Latest]         │
└───────────────────────┴──────────────────────────┘
```

右栏三块内容的来源：

- **About** —— 简介取 `repository.json` 里的 `description`，没写就退回到最新提交的标题；许可证是扫根目录找 `LICENSE` / `COPYING` / `UNLICENSE`，按内容特征串识别类型（MIT、Apache-2.0、GPL 系、BSD 系、ISC、MPL、Unlicense 等），认不出就只显示文件名。
- **Releases** —— 没有真正的 release 概念，用标签近似，按版本号排序，最高的标 `Latest`。
- **统计** —— pack 内对象数、分支数、标签数。

### 文档区

文件列表下方是可切换的文档区。目录里只要存在下面这些文件，就会自动变成一排 tab：

```
readme  license  changelog  contributing  code_of_conduct
security  governance  citation  authors  support
```

默认打开 README。`.md` / `.markdown` 按 Markdown 渲染，其余（`LICENSE`、`COPYING` 这类）
用等宽文本展示，保持原有的换行与缩进。向后缀不敏感，`README`、`README.md`、`README.rst`
都能识别。

标签顺序固定：README 永远在第一个，许可证第二，其余按名称排。子目录页也适用 ——
进到 `docs/` 目录时，那里如果有 README，同样会作为文档区展示。

标签排序用了版本号感知的比较（`localeCompare` 带 `numeric: true`），否则 `v10.0.0` 会排在 `v9.0.0` 前面。

## 界面路由

因为静态托管没有服务端重写能力（Arweave 网关也不会把未知路径回落到 index.html），路由只能用 hash：

```
#/                        项目列表
#/p2ping                  项目概览（文件树 + README + About）
#/p2ping/tree/main/src    目录
#/p2ping/blob/main/src/main.rs
#/p2ping/commits/main     提交历史
#/p2ping/branches         分支
#/p2ping/tags             标签
```

hash 部分不会发给服务器，所以深链接随便用。

## 换肤

界面逻辑与样式是分开的。所有可调项都收在 `public/src/style.css` 顶部的 CSS 变量里：

```css
:root {
  --bg:          #ffffff;   /* 画布 */
  --bg-subtle:   #f6f8fa;   /* 面板头、顶栏 */
  --text:        #1f2328;
  --text-muted:  #59636e;
  --border:      #d1d9e0;
  --accent:      #0969da;   /* 链接 */
  --accent-mark: #fd8c73;   /* 选中 tab 的下划线 */
  --page-width:  1280px;
  --side-width:  296px;
  --mono:        ...;
  --sans:        ...;
}
```

配色取自 GitHub 的公开设计规范，但 CSS 全部是自己写的，不存在源码复制。要换成完全不同的视觉风格，改这段变量加上 `style.css` 下半部分的选择器即可。

深色模式跟随系统，也可以用 `<html data-theme="dark">` 强制。

要改文案或结构，动 `public/src/views.js`，它只负责拼 DOM。

## 技术说明

### 解压边界问题

浏览器的原生 `DecompressionStream` 对尾部多余字节零容忍：

```
TypeError: Trailing junk found after the end of the compressed stream
```

而 packfile 里每个对象的 zlib 流没有长度字段。解法是借助 pack index 的偏移表 —— 把所有对象偏移排序后，某个对象的字节区间就是 `[自身偏移, 下一个偏移)`，切出来刚好是完整压缩数据。所以 `.idx` 是必需件，不是可选优化。

### 模块划分

```
src/git/zlib.js      解压封装
src/git/idx.js       pack index v2 解析
src/git/pack.js      pack 读取、delta 递归
src/git/delta.js     delta 指令解码
src/git/objects.js   commit / tree / tag 结构
src/git/repo.js      仓库门面：refs / tree / blob / log
```

`RemoteRepo` 接受一个 `baseUrl`，指向 `xxx.git/`。给它注入自定义 `fetch` 就能在 Node 里跑，`scripts/selftest.mjs` 就是这么做的。

### 自测

```bash
node scripts/selftest.mjs tmp/bare.git
```

它会走一遍 pack 加载、refs 解析、tree 递归、blob 解压、提交历史。

## 已知限制

- **只读**。协议层面就没有 push 的路径，这也是它能用纯静态文件托管的原因。
- **整包下载**。packfile 会一次性载入内存。几 MB 的项目没问题，几十 MB 会卡。真要优化得解析 `.idx` 后按 Range 惰性取对象。
- **不支持 shallow clone**。`--depth` 依赖服务端裁剪历史，静态托管做不到。
- **不能选择性公开**。`info/refs` 会列出仓库里所有 refs。不想公开的分支要在打包前清掉。
- **平台可能要放行 `.git` 路径**。部分托管（尤其带 WAF 的）会拦截 `/.git/` 前缀。部署后先 curl 一下 `xxx.git/info/refs` 确认。如果被拦，把目录改名成 `xxx.repo` 之类，clone 地址相应变化即可（git 不要求 URL 以 `.git` 结尾）。

## 目录

`public/` 就是站点根，整个上传即可；根目录下只放工具和文档。

```
public/                    部署根
  index.html               入口
  repository.json          仓库清单（由脚本维护）
  p2ping.git/              裸仓库，一个项目一个
  src/
    main.js                启动与路由分发
    router.js              hash 路由
    store.js               repository.json 与仓库缓存
    views.js               各视图渲染
    ui.js                  DOM 构造与小工具
    markdown.js            极简 Markdown 渲染（已做 HTML 转义）
    style.css              主题层
    git/                   只读 git 解析器
      zlib.js  idx.js  pack.js  delta.js  objects.js  repo.js  util.js
scripts/                   不参与部署
  prepare-repo.py          生成可托管的裸仓库
  serve.mjs                本地静态服务器
  selftest.mjs             解析器自测
  probe.mjs                环境探测
README.md
```

要改外观，只动 `public/src/style.css`；要改结构或文案，动 `public/src/views.js`。
