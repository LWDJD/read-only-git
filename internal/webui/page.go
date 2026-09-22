package webui

import "net/http"

// DefaultPage 是 webui 的单页界面。
//
// 零依赖、内嵌，不联网加载任何资源。风格沿用签名页：系统字体、细边框、
// 中性色。目标是实用，不做动画与主题切换。
//
// 一条纪律：界面不缓存文件状态。任何写操作完成后重新拉 /api/state，
// 因为管理员可能绕过界面直接改目录，界面必须如实反映磁盘。
const DefaultPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>read-only-git · 维护台</title>
<style>
  /* 颜色全部走变量：深色模式只改这一组值。
     之前把颜色硬编码在各处，深色只覆盖了一部分，就会出现「字变白了、
     输入框底还是白的」这种事。 */
  :root {
    color-scheme: light dark;

    --bg: #f6f7f9;
    --fg: #1f2328;
    --panel: #ffffff;
    --border: #d0d7de;
    --border-soft: #eaeef2;
    --muted: #656d76;
    --field-bg: #ffffff;
    --field-border: #d0d7de;
    --chip: #eaeef2;
    --btn: #f6f8fa;
    --btn-hover: #eef1f4;
    --link: #0969da;
    --ok: #1a7f37;
    --err: #cf222e;
    --warn: #9a6700;
    --accent: #1f883d;
    --accent-strong: #1a7f37;
    --on-accent: #ffffff;
  }

  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #0d1117;
      --fg: #e6edf3;
      --panel: #161b22;
      --border: #30363d;
      --border-soft: #21262d;
      --muted: #8b949e;
      --field-bg: #0d1117;
      --field-border: #30363d;
      --chip: #21262d;
      --btn: #21262d;
      --btn-hover: #30363d;
      --link: #4493f8;
      --ok: #3fb950;
      --err: #f85149;
      --warn: #d29922;
      --accent: #238636;
      --accent-strong: #2ea043;
      --on-accent: #ffffff;
    }
  }

  * { box-sizing: border-box; }
  body {
    font: 14px/1.6 system-ui, -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif;
    margin: 0; padding: 0 0 40px; background: var(--bg); color: var(--fg);
  }
  header {
    padding: 14px 20px; border-bottom: 1px solid var(--border); display: flex;
    align-items: baseline; gap: 12px; flex-wrap: wrap;
  }
  header h1 { font-size: 16px; margin: 0; }
  .muted { color: var(--muted); font-size: 13px; }
  main { display: grid; grid-template-columns: 380px minmax(0, 1fr); gap: 16px; padding: 16px 20px; }
  @media (max-width: 900px) { main { grid-template-columns: 1fr; } }
  .panel {
    background: var(--panel); border: 1px solid var(--border); border-radius: 6px;
    padding: 12px 14px; margin-bottom: 14px;
  }
  .panel h2 { font-size: 13px; margin: 0 0 10px; letter-spacing: .02em; }
  label { display: block; font-size: 12px; margin: 8px 0 3px; color: var(--muted); }
  input, select {
    width: 100%; padding: 6px 8px; font: inherit; font-size: 13px;
    border: 1px solid var(--field-border); border-radius: 4px;
    background: var(--field-bg); color: var(--fg);
  }
  input::placeholder { color: var(--muted); opacity: .75; }
  .row { display: flex; gap: 8px; }
  .row > * { flex: 1; }
  button {
    font: inherit; font-size: 13px; padding: 6px 12px; margin-top: 10px;
    border: 1px solid var(--border); border-radius: 5px;
    background: var(--btn); color: var(--fg); cursor: pointer;
  }
  button:hover { background: var(--btn-hover); }
  button.primary { background: var(--accent); border-color: var(--accent-strong); color: var(--on-accent); }
  button.primary:hover { background: var(--accent-strong); }
  button:disabled { opacity: .55; cursor: default; }
  .tabs { display: flex; gap: 4px; margin-bottom: 6px; }
  .tabs button { flex: 1; margin-top: 0; }
  .tabs button.active { background: var(--accent); border-color: var(--accent-strong); color: var(--on-accent); }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 5px 6px; border-bottom: 1px solid var(--border-soft); }
  th { font-size: 12px; color: var(--muted); font-weight: 600; border-bottom-color: var(--border); }
  td.mono, .mono { font-family: ui-monospace, Consolas, monospace; font-size: 12px; }

  /* 文件树。缩进用 padding-left 表达层级，每层 14px。
     目录行可点收起；文件行是拖拽的落点。 */
  .tree { font-size: 13px; border-top: 1px solid var(--border); }
  .trow {
    display: flex; align-items: center; gap: 6px; padding: 3px 6px;
    border-bottom: 1px solid var(--border-soft);
  }
  .trow.dir { cursor: pointer; user-select: none; }
  .trow.dir:hover { background: var(--chip); }
  .twist { width: 10px; color: var(--muted); font-size: 10px; }
  .tname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tname.dirname { font-weight: 600; }
  .tsize { width: 74px; text-align: right; color: var(--muted); font-size: 12px; }
  .tdigest { width: 78px; color: var(--muted); font-family: ui-monospace, Consolas, monospace; font-size: 11px; }
  .tact { width: 56px; text-align: right; }
  .tact button { margin: 0; padding: 2px 8px; font-size: 12px; }
  .kids { }
  tr.drop { background: var(--chip); }
  .chip {
    display: inline-block; font-size: 11px; padding: 1px 6px; border-radius: 10px;
    background: var(--chip); margin-left: 6px;
  }
  .logbox {
    background: var(--panel); border: 1px solid var(--border); border-radius: 6px;
    margin: 0 20px; padding: 10px 12px; height: 220px; overflow: auto;
    font-family: ui-monospace, Consolas, monospace; font-size: 12px; white-space: pre-wrap;
    color: var(--fg);
  }
  .err { color: var(--err); }
  .ok { color: var(--ok); }
  .warn { color: var(--warn); }
  a { color: var(--link); }
  h2 { display: flex; align-items: center; justify-content: space-between; }
</style>
</head>
<body>
<header>
  <h1>read-only-git 维护台</h1>
  <span class="muted" id="sitePath">…</span>
  <span class="muted" style="margin-left:12px">钱包 <b id="walletState">未连接</b></span>
  <button id="connWallet" style="margin:0">连接钱包</button>
  <button id="refresh" style="margin:0">重新扫描</button>
</header>

<main>
  <div>
    <section class="panel" id="scaffoldPanel">
      <h2>站点骨架</h2>
      <p class="muted" id="scaffoldHint" style="margin:0 0 8px">…</p>
      <label>模板</label>
      <select id="tplSelect"></select>
      <label><input type="checkbox" id="tplOverwrite" style="width:auto"> 覆盖已存在的文件</label>
      <button class="primary" id="doSiteInit">铺开骨架</button>
    </section>

    <section class="panel">
      <h2>打包</h2>
      <label>源仓库（本地路径，或远端地址）</label>
      <input id="packSource" placeholder="D:\path\to\repo">
      <label>输出目录（站点根）</label>
      <div class="row">
        <input id="packOut" placeholder="public" value="public">
      </div>
      <label>仓库名（留空则从源推导）</label>
      <input id="packName" placeholder="myrepo">
      <label><input type="checkbox" id="packIncremental" style="width:auto"> 增量更新（保留旧 pack）</label>
      <button class="primary" id="doPack">开始打包</button>
    </section>

    <section class="panel">
      <h2>发布</h2>
      <div class="tabs" id="targetTabs">
        <button data-target="local">本地目录</button>
        <button data-target="turbo" class="active">Turbo</button>
        <button data-target="l1">L1</button>
      </div>
      <div id="destLocal" hidden>
        <label>目标目录</label>
        <input id="pubDest" placeholder="D:\path\to\out">
      </div>
      <div id="destTurbo">
        <label>上传服务</label>
        <input id="pubEndpoint" placeholder="https://turbo.ardrive.io">
      </div>
      <div id="destL1" hidden>
        <label>节点</label>
        <input id="pubNode" placeholder="https://arweave.net">
      </div>
      <label>网络出口（发布时访问节点与网关）</label>
      <div class="row">
        <select id="pubProxyMode" style="flex:1">
          <option value="system">跟随系统代理</option>
          <option value="manual">手动指定</option>
          <option value="off">不走代理</option>
        </select>
        <input id="pubProxyUrl" placeholder="http://127.0.0.1:7890" style="flex:2">
      </div>
      <label>从链上恢复（可选，填入口 id）</label>
      <input id="pubFrom" placeholder="re22tX-…">
      <p class="muted" style="margin:0">仓库名取站点目录名，不用填。</p>
      <button class="primary" id="doPublish">开始发布</button>
    </section>

    <section class="panel">
      <h2>记录</h2>
      <table>
        <thead><tr><th>记录</th><th>目标</th><th>引用</th><th>更新时间</th></tr></thead>
        <tbody id="records"></tbody>
      </table>
    </section>
  </div>

  <div>
    <section class="panel">
      <h2>
        <span>站点文件</span>
        <span class="muted" id="fileSummary"></span>
      </h2>
      <p class="muted" style="margin:0 0 8px">把文件或整个目录拖进来：拖到文件行替换该文件，拖到目录行落进该目录，拖到空白处落到站点根。重名会先问一次。</p>
      <div class="tree" id="files"></div>
    </section>
  </div>
</main>

<div class="logbox" id="logs">task 日志会显示在这里。</div>

<script src="/sign/vendor/arweave.js"></script>
<script>
(function () {
  var site = '';
  var activeTarget = 'turbo';

  // token 从地址栏取，所有请求都带上它。
  // 服务只绑本机，但同机的任意网页都能向它发请求，靠这一层挡住别的页面。
  var TOKEN = new URLSearchParams(location.search).get('token') || '';

  function api(path, opts) {
    var o = Object.assign({}, opts || {});
    o.headers = Object.assign({}, o.headers || {}, { 'X-Rog-Token': TOKEN });
    return fetch(path, o);
  }

  function el(id) { return document.getElementById(id); }

  function fmtSize(n) {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KiB';
    return (n / 1024 / 1024).toFixed(2) + ' MiB';
  }

  function log(line, cls) {
    var box = el('logs');
    var span = document.createElement('div');
    if (cls) span.className = cls;
    span.textContent = line;
    box.appendChild(span);
    box.scrollTop = box.scrollHeight;
  }

  function clearLog() { el('logs').textContent = ''; }

  // 站点骨架：把内嵌的前端模板写到站点目录。
  // 只有一个 exe 时靠它把站点立起来。
  function renderScaffold(st) {
    var sc = st.scaffold || {};
    var tpls = sc.templates || [];
    var sel = el('tplSelect');

    // 只在列表变化时重建，否则每次刷新都会把用户选的模板冲掉
    var ids = tpls.map(function (t) { return t.id; }).join(',');
    if (sel.dataset.ids !== ids) {
      sel.textContent = '';
      tpls.forEach(function (t) {
        var opt = document.createElement('option');
        opt.value = t.id;
        opt.textContent = t.name + '（' + t.files + ' 个文件）';
        sel.appendChild(opt);
      });
      sel.dataset.ids = ids;
    }

    var missing = sc.missing || 0;
    var total = sc.total || 0;
    var hint = el('scaffoldHint');

    if (!st.exists) {
      hint.textContent = '站点目录还不存在，铺开骨架会把它一并建好（' + total + ' 个文件）。';
      hint.className = 'muted';
    } else if (missing > 0) {
      hint.textContent = '还缺 ' + missing + ' / ' + total + ' 个骨架文件，界面现在打不开。';
      hint.className = 'warn';
    } else {
      hint.textContent = '骨架完整（' + total + ' 个文件）。';
      hint.className = 'muted';
    }
  }

  // 拉一次状态并重画。任何写操作之后都要调用它，不做乐观更新。
  // ---- 钱包签名 ----
  //
  // 发布到 Arweave 时，Go 侧把待签的内容摆在 /sign/ 下等着，
  // 这里取出来交给钱包、把结果送回去。
  // 与 CLI 那条路共用同一套端点，只是页面换成了当前这个。

  function wallet() { return window.arweaveWallet; }

  function delay(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  // 钱包扩展是异步注入 window.arweaveWallet 的，可能晚于本脚本执行
  function waitForWallet(ms) {
    return new Promise(function (resolve, reject) {
      var deadline = Date.now() + ms;
      (function poll() {
        if (wallet()) return resolve(wallet());
        if (Date.now() > deadline) return reject(new Error('没检测到 Arweave 钱包扩展'));
        setTimeout(poll, 150);
      })();
    });
  }

  // 让钱包给一笔「data 就是这一整包」的交易签名。
  //
  // arweave-js 在这里只做两件事：构造交易（算 data_root、reward、last_tx），
  // 以及把交易交给钱包签。签完只回传字段，包体不动，
  // 免得整份内容再多走一趟 base64。
  async function signBundleTransaction(buf, tags) {
    if (!window.Arweave) throw new Error('arweave-js 没加载出来');
    var arweave = window.Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });
    var tx = await arweave.createTransaction({ data: new Uint8Array(buf) });
    var list = tags || [];
    for (var i = 0; i < list.length; i++) {
      tx.addTag(list[i].name, list[i].value);
    }
    // 省略 JWK 参数时 arweave-js 会走注入的钱包
    await arweave.transactions.sign(tx);

    // proofs 供 Go 侧走分块上传。超过一块时交易 JSON 不带 data，
    // 由 Go 按同样的切法逐块发 /chunk。
    // 不回传块内容本身：那等于把整包再传一遍。
    var chunkProofs = [];
    var proofList = (tx.chunks && tx.chunks.proofs) || [];
    for (var k = 0; k < proofList.length; k++) {
      chunkProofs.push({
        data_path: toB64Url(proofList[k].proof),
        offset: String(proofList[k].offset),
      });
    }

    return JSON.stringify({
      id: tx.id,
      owner: tx.owner,
      signature: tx.signature,
      reward: tx.reward,
      last_tx: tx.last_tx,
      // data_root 是签名内容的一部分，Go 侧要拿它拼交易 JSON。
      data_root: tx.data_root,
      data_size: tx.data_size,
      proofs: chunkProofs,
    });
  }

  // base64url 编码，不带填充。
  //
  // 优先用 arweave-js 的；取不到就自己编一份，
  // 免得因为一个工具函数让整条分块路径在某个版本上失效。
  function toB64Url(bytes) {
    var u = window.Arweave && window.Arweave.utils;
    if (u && typeof u.bufferTob64Url === 'function') {
      return u.bufferTob64Url(bytes);
    }
    var bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  // 签名循环的停止标志。任务一结束就置上，循环自己退出，
  // 免得多转几圈去问一个已经没人应答的端点。
  var signStop = false;

  function stopSigning() { signStop = true; }

  async function connectWallet() {
    log('正在等待钱包扩展…');
    try {
      var w = await waitForWallet(30000);
      await w.connect(['ACCESS_ADDRESS', 'SIGNATURE', 'SIGN_TRANSACTION']);
      el('walletState').textContent = '已连接';
      el('walletState').className = 'ok';
      log('已连接钱包', 'ok');
    } catch (e) {
      var msg = e && e.message ? e.message : String(e);
      el('walletState').textContent = '未连接';
      el('walletState').className = 'err';
      log(msg + '。请安装并启用 Wander。', 'err');
    }
  }

  // runSignLoop 反复问 /sign/api/next，把待签内容交给钱包。
  async function runSignLoop() {
    signStop = false;
    var n = 0;

    while (!signStop) {
      var task = null;
      try {
        var res = await api('/sign/api/next', { cache: 'no-store' });
        task = await res.json();
      } catch (e) {
        // 端点暂时问不到，多半是任务刚收尾。稍后再试，
        // 由 signStop 决定要不要继续。
        await delay(400);
        continue;
      }

      if (!task || !task.id) {
        // 暂时没有待签内容，而不是结束了：后端是串行准备的，中间会有空窗
        await delay(300);
        continue;
      }

      try {
        var blobRes = await api('/sign/api/blob/' + task.id);
        if (!blobRes.ok) throw new Error('取内容失败：' + blobRes.status);
        var buf = await blobRes.arrayBuffer();

        // 两类任务的产物不同：
        //   dataitem 回传签名字节
        //   tx       回传交易的签名字段
        // 另外，钱包的 signDataItem 只接受 string 或 Uint8Array，
        // 直接递 ArrayBuffer 会被它内部的断言挡下。
        var signed = task.kind === 'tx'
          ? await signBundleTransaction(buf, task.tags)
          : await wallet().signDataItem({ data: new Uint8Array(buf), tags: task.tags });

        await api('/sign/api/sign/' + task.id, { method: 'POST', body: signed });
      } catch (e) {
        log('签名失败：' + (e && e.message ? e.message : String(e)), 'err');
        return;
      }

      n += 1;
      log('已签名 ' + n + ' 个');
    }
  }

  async function refresh() {
    var res = await api('/api/state');
    var st = await res.json();

    site = st.site;
    el('sitePath').textContent = st.site;

    renderScaffold(st);

    if (st.error) {
      log(st.error, 'err');
    }

    renderFiles(st.files || []);
    el('fileSummary').textContent = (st.files || []).length + ' 个文件 · ' + fmtSize(st.totalSize || 0);

    var rb = el('records');
    rb.textContent = '';
    (st.records || []).forEach(function (r) {
      var tr = document.createElement('tr');
      [r.file, r.target, String(r.count), r.updated].forEach(function (v) {
        var td = document.createElement('td');
        td.textContent = v;
        tr.appendChild(td);
      });
      rb.appendChild(tr);
    });
  }

  // 把扁平的路径清单聚成树。
  //
  // 后端给的是一条条 path，层级是纯展示需求，所以在这一层聚合，
  // 不去动后端那份「发布要用的原始数据」。
  function buildTree(files) {
    var root = { dirs: {}, files: [] };
    files.forEach(function (f) {
      var parts = f.path.split('/');
      var node = root;
      for (var i = 0; i < parts.length - 1; i++) {
        var d = parts[i];
        if (!node.dirs[d]) node.dirs[d] = { name: d, dirs: {}, files: [] };
        node = node.dirs[d];
      }
      node.files.push({ name: parts[parts.length - 1], entry: f });
    });
    return root;
  }

  function countFiles(node) {
    var n = node.files.length;
    Object.keys(node.dirs).forEach(function (d) { n += countFiles(node.dirs[d]); });
    return n;
  }

  function sumSize(node) {
    var n = 0;
    node.files.forEach(function (f) { n += f.entry.size; });
    Object.keys(node.dirs).forEach(function (d) { n += sumSize(node.dirs[d]); });
    return n;
  }

  function fileRow(entry, depth) {
    var row = document.createElement('div');
    row.className = 'trow';
    row.dataset.path = entry.path;
    row.style.paddingLeft = (6 + depth * 14) + 'px';

    var name = document.createElement('span');
    name.className = 'tname mono';
    name.textContent = entry.path.split('/').pop();

    var size = document.createElement('span');
    size.className = 'tsize';
    size.textContent = fmtSize(entry.size);

    var dig = document.createElement('span');
    dig.className = 'tdigest';
    dig.textContent = (entry.digest || '').slice(0, 8);

    var act = document.createElement('span');
    act.className = 'tact';
    var del = document.createElement('button');
    del.textContent = '删除';
    del.onclick = function (e) { e.stopPropagation(); removeFile(entry.path); };
    act.appendChild(del);

    row.appendChild(name); row.appendChild(size); row.appendChild(dig); row.appendChild(act);
    attachFileDrop(row);
    return row;
  }

  function dirRow(node, dirPath, depth, box) {
    var row = document.createElement('div');
    row.className = 'trow dir';
    row.style.paddingLeft = (6 + depth * 14) + 'px';

    var twist = document.createElement('span');
    twist.className = 'twist';
    twist.textContent = '▾';

    var name = document.createElement('span');
    name.className = 'tname dirname';
    name.textContent = node.name + '/';

    var size = document.createElement('span');
    size.className = 'tsize';
    size.textContent = fmtSize(sumSize(node));

    var count = document.createElement('span');
    count.className = 'tdigest';
    count.textContent = countFiles(node) + ' 个';

    row.appendChild(twist); row.appendChild(name); row.appendChild(size); row.appendChild(count);

    var kids = document.createElement('div');
    kids.className = 'kids';
    renderNode(node, dirPath, depth + 1, kids);

    row.onclick = function () {
      var open = !kids.hidden;
      kids.hidden = open;
      twist.textContent = open ? '▸' : '▾';
    };

    // 拖到这个目录行上，文件落在它里面
    attachDirDrop(row, dirPath);

    box.appendChild(row);
    box.appendChild(kids);
  }

  function renderNode(node, dirPath, depth, box) {
    Object.keys(node.dirs).sort().forEach(function (d) {
      var child = node.dirs[d];
      dirRow(child, dirPath ? dirPath + '/' + d : d, depth, box);
    });
    node.files.slice().sort(function (a, b) { return a.name < b.name ? -1 : 1; }).forEach(function (f) {
      box.appendChild(fileRow(f.entry, depth));
    });
  }

  function renderFiles(files) {
    var box = el('files');
    box.textContent = '';

    // 查重名要用当前磁盘上的路径，在这里更新，refresh 之后就准了
    currentPaths = {};
    files.forEach(function (f) { currentPaths[f.path] = true; });

    renderNode(buildTree(files), '', 0, box);
  }

  // 拖放。落点按拖到哪决定：
  //
  //   拖到文件行   → 替换该文件
  //   拖到目录行   → 落到该目录下，各按自己的相对路径
  //   拖到面板空白 → 落到站点根
  //
  // 重名不逐个问：拖二十个文件会弹二十次窗。收集齐之后统一问一次，
  // 确定 = 替换这一批里的重名，取消 = 跳过它们、只写新文件。

  // 当前磁盘上的路径集合，用来查重名。renderFiles 时更新。
  var currentPaths = {};

  // collectDrops 把一次拖放里的东西收成 [{rel, file}]。
  //
  // 走 webkitGetAsEntry：拖目录时 dataTransfer.files 是空的，
  // 只有它能把目录递归展开。拿不到时退化成平铺的文件清单，
  // 那种情况下没有目录结构可用，只能按文件名落位。
  async function collectDrops(dt) {
    var out = [];
    var items = dt.items;

    if (!items || !items.length) {
      Array.prototype.forEach.call(dt.files || [], function (f) {
        out.push({ rel: f.name, file: f });
      });
      return out;
    }

    for (var i = 0; i < items.length; i++) {
      if (items[i].kind !== 'file') continue;
      var entry = items[i].webkitGetAsEntry && items[i].webkitGetAsEntry();
      if (entry) {
        if (entry.isDirectory) {
          // 最外层目录的名字不保留：它叫什么由落点决定，
          // 它内部的层级关系才是要带过去的。
          await readDirInto(entry, '', out);
        } else {
          await walkEntry(entry, '', out);
        }
      } else if (items[i].getAsFile) {
        var f = items[i].getAsFile();
        if (f) await walkEntry(null, f.name, out, f);
      }
    }
    return out;
  }

  // readDirInto 把一个目录的内容读进来，prefix 是这些内容对应的相对路径前缀。
  async function readDirInto(dir, prefix, out) {
    var reader = dir.createReader();
    // readEntries 一次只给一批，要反复读到空为止
    for (;;) {
      var batch = await new Promise(function (res, rej) { reader.readEntries(res, rej); });
      if (!batch.length) break;
      for (var i = 0; i < batch.length; i++) {
        await walkEntry(batch[i], prefix, out);
      }
    }
  }

  // walkEntry 处理拖入内容里的一个条目。
  //
  // prefix 是它所在层级的路径前缀（不含自己的名字），
  // 自己的名字在这里拼，往上只能拼一次。
  async function walkEntry(entry, prefix, out, plainFile) {
    if (!entry) {
      if (plainFile) out.push({ rel: prefix, file: plainFile });
      return;
    }
    if (entry.isFile) {
      var f = await new Promise(function (res, rej) { entry.file(res, rej); });
      out.push({ rel: prefix ? prefix + '/' + f.name : f.name, file: f });
      return;
    }
    if (!entry.isDirectory) return;

    // 内层目录的名字要并进前缀，供它里面的文件使用
    await readDirInto(entry, prefix ? prefix + '/' + entry.name : entry.name, out);
  }

  // readAsB64 把文件读成 base64。
  //
  // 分块拼接，避免一次性 apply 超长数组把调用栈撑爆（大文件真的会）。
  async function readAsB64(file) {
    var buf = await file.arrayBuffer();
    var bytes = new Uint8Array(buf);
    var bin = '';
    var CHUNK = 0x8000;
    for (var i = 0; i < bytes.length; i += CHUNK) {
      bin += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK));
    }
    return btoa(bin);
  }

  // dropInto 处理一次拖放。base 是落点的目录前缀，空串表示站点根。
  async function dropInto(base, dt) {
    var items = await collectDrops(dt);
    if (!items.length) return;

    var planned = items.map(function (it) {
      return { path: base ? base + '/' + it.rel : it.rel, file: it.file };
    });

    var clashes = planned.filter(function (p) { return currentPaths[p.path]; });
    var overwrite = true;
    if (clashes.length) {
      var sample = clashes.slice(0, 3).map(function (p) { return p.path; }).join('、');
      var more = clashes.length > 3 ? ' 等 ' + clashes.length + ' 个' : '';
      overwrite = confirm(
        '有 ' + clashes.length + ' 个路径已存在：' + sample + more + '\n\n' +
        '确定 = 替换它们；取消 = 跳过它们，只写新文件。'
      );
    }

    var files = [];
    for (var i = 0; i < planned.length; i++) {
      var p = planned[i];
      if (clashes.length && !overwrite && currentPaths[p.path]) continue;
      files.push({ path: p.path, bytes: await readAsB64(p.file) });
    }
    if (!files.length) { log('全部跳过，没有写入', 'warn'); return; }
    if (files.length > 100) { log('一批写了 ' + files.length + ' 个文件，可能要等一会'); }

    var res = await api('/api/files/replace', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, files: files }),
    });
    var out = await res.json();
    if (!res.ok) { log('写入失败：' + (out.error || res.status), 'err'); return; }

    // 逐个报结果：一批里部分失败时，不能只说「写完了」
    var ok = 0, bad = [];
    (out.results || []).forEach(function (r) {
      if (r.error) bad.push(r.path + '（' + r.error + '）');
      else ok++;
    });
    if (ok) log('已写入 ' + ok + ' 个文件', 'ok');
    if (bad.length) log('有 ' + bad.length + ' 个没写成：\n  ' + bad.join('\n  '), 'err');
    refresh();
  }

  // attachFileDrop：拖到某一行上，替换那一行的文件。
  function attachFileDrop(row) {
    row.addEventListener('dragover', function (e) {
      e.preventDefault();
      row.classList.add('drop');
    });
    row.addEventListener('dragleave', function () { row.classList.remove('drop'); });
    row.addEventListener('drop', async function (e) {
      e.preventDefault();
      e.stopPropagation();
      row.classList.remove('drop');

      var items = await collectDrops(e.dataTransfer);
      if (!items.length) return;
      if (items.length > 1) {
        log('拖到了文件行上，只取第一个：' + items[0].rel, 'warn');
      }

      var b64 = await readAsB64(items[0].file);
      var res = await api('/api/files/replace', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ site: site, files: [{ path: row.dataset.path, bytes: b64 }] }),
      });
      var out = await res.json();
      if (!res.ok) { log('替换失败：' + (out.error || res.status), 'err'); return; }
      var r = (out.results || [])[0] || {};
      if (r.error) { log('替换失败：' + r.error, 'err'); return; }
      log('已替换 ' + row.dataset.path + '（' + fmtSize(r.size || 0) + '）', 'ok');
      refresh();
    });
  }

  // attachDirDrop：拖到目录行（或面板空白）上，文件落到那个目录里。
  function attachDirDrop(row, base) {
    row.addEventListener('dragover', function (e) {
      e.preventDefault();
      e.stopPropagation();
      row.classList.add('drop');
    });
    row.addEventListener('dragleave', function () { row.classList.remove('drop'); });
    row.addEventListener('drop', function (e) {
      e.preventDefault();
      e.stopPropagation();
      row.classList.remove('drop');
      dropInto(base, e.dataTransfer);
    });
  }

  async function removeFile(path) {
    var res = await api('/api/files/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, path: path }),
    });
    var out = await res.json();
    if (!res.ok) { log('删除失败：' + (out.error || res.status), 'err'); return; }
    log('已删除 ' + path, 'ok');
    refresh();
  }

  // 最近一次发起的任务参数。失败后「重试」就是拿它原样再发一次。
  //
  // 不从输入框重新收集：重试的语义是「刚才那一次再来一遍」，
  // 用户中途改过的输入不该混进来。要换参数就重新点那个按钮。
  var lastTask = null;

  // offerRetry 失败后给一个重试按钮。
  //
  // 不自动重试：失败原因分两类，网络抖动值得再试，
  // 「站点里没有文件」这类再试多少次都一样。让用户自己判断。
  function offerRetry() {
    if (!lastTask) return;
    var btn = document.createElement('button');
    btn.className = 'retry';
    btn.textContent = '重试';
    btn.onclick = function () {
      if (!lastTask) return;
      // 清掉旧的按钮，免得连点之后堆一列
      Array.prototype.forEach.call(el('logs').querySelectorAll('button.retry'), function (b) { b.remove(); });
      runTask(lastTask.url, lastTask.body, lastTask.label);
    };
    el('logs').appendChild(btn);
  }

  // 所有耗时操作都走这里：先拿 taskId，再订阅 SSE 看进度。
  async function runTask(url, body, label) {
    clearLog();
    log('> ' + label);
    lastTask = { url: url, body: body, label: label };

    var res = await api(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    var out = await res.json();
    if (!res.ok) { log('发起失败：' + (out.error || res.status), 'err'); return; }
    if (!out.taskId) { log('后端没有返回 taskId', 'err'); return; }

    // EventSource 不能自定义请求头，token 只能跟在 URL 上
    var src = new EventSource('/api/task/' + out.taskId + '/events?token=' + encodeURIComponent(TOKEN));
    var seen = 0;

    src.onmessage = function (ev) {
      log(ev.data);
      seen++;
    };
    src.addEventListener('end', function () {
      src.close();
      // 任务结束，签名循环也该退了，不然它会一直问一个不再有内容的端点
      stopSigning();
      // 结束后拉一次状态与任务结果
      api('/api/task/' + out.taskId)
        .then(function (r) { return r.json(); })
        .then(function (t) {
          if (t.status === 'failed') {
            log('失败：' + (t.error || '未知错误'), 'err');
            offerRetry();
          } else {
            log('完成', 'ok');
            lastTask = null;
          }
          refresh();
        });
    });
    src.onerror = function () { src.close(); };
  }

  el('refresh').onclick = refresh;
  el('connWallet').onclick = connectWallet;

  // 文件树本身就是兜底的拖放区：拖到空白处（不是某一行）就落到站点根
  attachDirDrop(el('files'), '');

  // 拖到页面别处时，浏览器默认会直接打开这个文件。一律拦掉。
  document.addEventListener('dragover', function (e) { e.preventDefault(); });
  document.addEventListener('drop', function (e) { e.preventDefault(); });

  Array.prototype.forEach.call(el('targetTabs').children, function (btn) {
    btn.onclick = function () {
      Array.prototype.forEach.call(el('targetTabs').children, function (b) { b.classList.remove('active'); });
      btn.classList.add('active');
      activeTarget = btn.dataset.target;
      el('destLocal').hidden = activeTarget !== 'local';
      el('destTurbo').hidden = activeTarget !== 'turbo';
      el('destL1').hidden = activeTarget !== 'l1';
    };
  });

  el('doSiteInit').onclick = function () {
    runTask('/api/site/init', {
      site: site,
      template: el('tplSelect').value,
      overwrite: el('tplOverwrite').checked,
    }, '铺开站点骨架');
  };

  el('doPack').onclick = function () {
    runTask('/api/pack', {
      source: el('packSource').value.trim(),
      outDir: el('packOut').value.trim(),
      name: el('packName').value.trim(),
      incremental: el('packIncremental').checked,
    }, '打包');
  };

  el('doPublish').onclick = function () {
    // 两条 Arweave 路都要钱包签名。在本页面里直接把签名循环跑起来，
    // 不再另开一个标签页。
    if (activeTarget !== 'local') {
      runSignLoop();
    }
    runTask('/api/publish', {
      site: site,
      target: activeTarget,
      dest: el('pubDest').value.trim(),
      endpoint: el('pubEndpoint').value.trim(),
      node: el('pubNode').value.trim(),
      from: el('pubFrom').value.trim(),
      proxyMode: el('pubProxyMode').value,
      proxyUrl: el('pubProxyUrl').value.trim(),
    }, '发布到 ' + activeTarget);
  };

  refresh();
})();
</script>
</body>
</html>
`

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(DefaultPage))
}
