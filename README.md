# read-only-git

把裸仓库丢到静态托管上，访客就能 `git clone`，同时有一个网页界面浏览文件树、README 和提交历史。

零运行时依赖，零构建步骤。打开 `index.html` 就能跑。

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

### 1. 生成可托管的仓库

```
python scripts/prepare-repo.py <源仓库> [输出目录] [仓库名]
```

| 参数 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `<源仓库>` | 是 | | 要转换的仓库路径，普通仓库和裸仓库都行 |
| `[输出目录]` | 否 | 脚本旁边的 `../public` | 站点根目录，也就是放着 `index.html` 的那个 |
| `[仓库名]` | 否 | 源目录名 | 对外标识，带不带 `.git` 后缀等价 |

尖括号是必填，方括号是可省略。三个参数按位置传，没有选项开关，`-h` 看用法。

```bash
# 只给源仓库，其余两个用默认值
python scripts/prepare-repo.py ../p2ping

# 指定输出目录
python scripts/prepare-repo.py ../p2ping site

# 三个都指定
python scripts/prepare-repo.py ../p2ping site my-repo
```

只依赖 Python 3 标准库和 PATH 里的 git，Windows / macOS / Linux 通用。

#### 为什么输出目录要指向站点根

脚本只生成裸仓库和 `repository.json`，不碰前端。所以输出目录得是**放着 `index.html` 的那个目录**，否则产物没法直接部署：

```
你的项目/
  public/                 ← 站点根
    index.html            ┐ 前端，仓库还没下载时就得有
    src/                  ┘
    p2ping.git/           ← 脚本写进来的
    repository.json       ← 脚本维护的
```

往空目录里跑也能生成，但脚本会在结尾提醒你还缺 `index.html`。第一次搭建时先把站点骨架准备好，再往里面写仓库。

#### 它做的事

裸仓库输出到 `<输出目录>/<仓库名>.git/` → `repack -a -d` 把对象收进单 pack → `gc --prune=now` → `update-server-info` 生成索引 → 清掉协议用不到的文件 → 校验并打印清单 → 更新 `repository.json`。

重复执行安全：同名仓库覆盖重建，`repository.json` 里那条的 `description` 会保留。

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

在沙箱或受限的 `sh.exe` 环境下，本地传输建不出管道，`clone --bare` 会报 `couldn't create signal pipe`。脚本这时自动改用 bundle：

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

### 2. 本地预览

```
node scripts/serve.mjs [根目录] [端口]
```

| 参数 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `[根目录]` | 否 | 脚本旁边的 `../public` | 要服务的目录 |
| `[端口]` | 否 | `4173` | |

```bash
node scripts/serve.mjs                        # http://localhost:4173/
node scripts/serve.mjs site 4200              # 换目录和端口
```

根目录取自脚本自身位置，所以在哪个目录调用都行。行为跟真实静态托管对齐，包括对 Range 请求的支持（dumb 协议会用它做局部下载）。

### 3. 部署

把 `public/` 整个上传。没有构建产物需要生成。

- **Arweave / IPFS**：上传目录并生成 path manifest，用 manifest 的 tx id / CID 访问
- **Cloudflare Pages / EdgeOne**：绑定仓库或上传目录
- **nginx**：`root` 指到 `public/`

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
- **源仓库不能是浅克隆**。`git clone --depth` 拉下来的历史不完整，`repack` 遍历父提交时会失败。脚本会提前拦住并提示 `fetch --unshallow`
- **整包下载**。packfile 一次性载入内存，几十 MB 会卡。要优化得解析 `.idx` 后按 Range 惰性取对象
- **不支持 shallow clone**。`--depth` 依赖服务端裁剪历史
- **不能选择性公开**。`info/refs` 列出仓库里所有 refs，不想公开的分支要在打包前清掉
- **平台可能拦 `.git` 路径**。带 WAF 的托管会挡 `/.git/` 前缀，部署后先 curl 一下 `xxx.git/info/refs` 确认。被拦就把目录改名成 `xxx.repo` 之类，clone 地址相应变化（git 不要求 URL 以 `.git` 结尾）

## 目录

```
public/                    部署根，整个上传
  index.html               入口
  repository.json          仓库清单（由脚本维护）
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
  prepare-repo.py          生成可托管的裸仓库
  serve.mjs                本地静态服务器
  selftest.mjs             解析器自测
  probe.mjs                环境探测
```

改外观只动 `public/src/style.css`，改结构或文案动 `public/src/views.js`。
