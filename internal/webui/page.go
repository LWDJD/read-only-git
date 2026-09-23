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

  /* 文件面板：像资源管理器那样，一次只列一层。 */
  .tree { font-size: 13px; border-top: 1px solid var(--border); }
  .trow {
    display: flex; align-items: center; gap: 6px; padding: 3px 6px;
    border-bottom: 1px solid var(--border-soft);
  }
  .trow.dir, .trow.up { cursor: pointer; user-select: none; }
  .trow.dir:hover, .trow.up:hover { background: var(--chip); }
  .trow.sel { background: var(--chip); }
  .twist { width: 10px; color: var(--muted); font-size: 10px; }
  .tname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tname.dirname { font-weight: 600; }
  .tsize { width: 74px; text-align: right; color: var(--muted); font-size: 12px; }
  .tdigest { width: 78px; color: var(--muted); font-family: ui-monospace, Consolas, monospace; font-size: 11px; }
  .tact { width: 128px; text-align: right; white-space: nowrap; }
  .tact button { margin: 0 0 0 4px; padding: 2px 8px; font-size: 12px; }
  .tree.drop { outline: 2px dashed var(--accent); outline-offset: -2px; }
  .crumbs { margin-bottom: 8px; font-size: 13px; line-height: 1.8; }
  .crumbs a { text-decoration: none; }
  .crumbs a:hover { text-decoration: underline; }
  .filesbar { display: flex; gap: 6px; align-items: center; margin-bottom: 8px; flex-wrap: wrap; }
  .filesbar button { margin: 0; padding: 3px 10px; font-size: 12px; }
  .filesbar .spacer { flex: 1; }
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
      <label>远端源代理（可选，拉远端仓库时用）</label>
      <input id="packProxy" placeholder="http://127.0.0.1:7890">
      <label><input type="checkbox" id="packRebuild" style="width:auto"> 完整重打包（忽略已有产物，从零重建）</label>
      <p class="muted" style="margin:4px 0 0">默认自动：站点里已有这个仓库就做增量，只传变化的文件。</p>
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
      <p class="muted" style="margin:0 0 8px">像资源管理器一样逐层进入。把文件或整个目录拖进面板，就落在当前目录里；同名的会先问一次。</p>
      <div class="filesbar">
        <button id="fileUp">上一层</button>
        <button id="fileCopy">复制选中</button>
        <button id="filePaste">粘贴</button>
        <button id="fileMkdir">新建文件夹</button>
        <span class="spacer"></span>
        <span class="muted" id="clipInfo"></span>
      </div>
      <div class="crumbs" id="crumbs"></div>
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

  // 当前任务 id。note 要拿它把消息回传给后端。
  var currentTaskId = '';

  // note 把一条消息同时写到界面与任务日志。
  //
  // 为什么要回传：日志文件只收后端的 Logf，而像「签名失败：…」
  // 这种话是前端写的。不回传的话，出了事翻日志，
  // 最关键的那句偏偏不在——实测就是如此。
  //
  // 只用在关键处（失败、警告、阶段性结果）。逐条回传「已签名 N 个」
  // 会把日志刷得看不清东西。
  function note(line, cls) {
    log(line, cls);
    if (!currentTaskId) return;
    api('/api/task/' + currentTaskId + '/note', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ line: line }),
    }).catch(function () { /* 记日志失败不该影响正事 */ });
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

  // reward 要在节点给的最低价上抬一手。
  //
  // 为什么必须抬：/price 返回的是「最低可接受价」，而节点把交易收进
  // mempool 后是按 reward 排序的——见 ar_tx:utility/1，v2 交易的优先级
  // 就是 {2, Denomination, Reward}，连数据大小都不看。mempool 一满，
  // ar_mempool:find_low_priority_txs/2 就丢最低的那批。
  //
  // 付最低价 = 优先级垫底 = 网络一忙第一个被挤掉。
  // 而 POST /tx 那一步早就返回 200 了，响应里看不出任何异常，
  // 现象就是「提示上传成功、链上从此查不到」。
  //
  // 系数不大，目的是「别垫底」而不是「抢着打包」。
  var REWARD_MULTIPLIER = 2;

  // bumpReward 把 arweave-js 算出的最低价抬一档。
  // 必须在签名之前调：reward 是签名输入的一项，签完再改就对不上了。
  function bumpReward(tx) {
    try {
      var base = BigInt(tx.reward);
      if (base > 0n) {
        tx.reward = (base * BigInt(REWARD_MULTIPLIER)).toString();
      }
    } catch (e) {
      // 算不出来就维持原值。「抬一手」是优选项，不值得为此把发布搞停。
    }
  }

  // 让钱包签一笔「data 就是这一整包」的交易，然后自己提交上链。
  //
  // 为什么提交也放在这里：署名用的对象与提交出去的对象是同一个，
  // 就不存在「两处各自拼出来的交易 JSON 是否等价」这个问题。
  // 之前是 Go 那边拿回字段自己拼 JSON 再提交，一旦有哪一项对不上，
  // 节点只会回一句 verification failed，很难查。
  //
  // 中间那一次 verify 也是这个用意：先把「签名本身对不对」
  // 与「提交环节对不对」分开，出错时才能知道是哪一头。
  async function signAndUploadBundle(buf, tags) {
    if (!window.Arweave) throw new Error('arweave-js 没加载出来');
    var arweave = window.Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });

    var tx = await arweave.createTransaction({ data: new Uint8Array(buf) });
    var list = tags || [];
    for (var i = 0; i < list.length; i++) {
      tx.addTag(list[i].name, list[i].value);
    }
    // 抬价要在签名之前，reward 是签名输入的一项。
    bumpReward(tx);
    // 省略 JWK 参数时 arweave-js 会走注入的钱包
    await arweave.transactions.sign(tx);

    // 自验：签名与签名输入对不对得上。不过就说明钱包给的东西有问题，
    // 这一步能把它与「提交环节的问题」当场分开。
    var ok = false;
    try { ok = await arweave.transactions.verify(tx); } catch (e) { ok = false; }
    if (!ok) {
      throw new Error('签名自验没过：钱包返回的 owner / signature 与这笔交易的签名输入对不上');
    }

    // 提交。
    //
    // 单块时自己 POST，不用 upload()：需要看到节点到底回了什么。
    // 实测碰到的正是「upload 说成功、链上却查不到」，
    // 而 upload 不暴露响应体，一出这种情形就无从判断。
    var chunkCount = (tx.chunks && tx.chunks.chunks) ? tx.chunks.chunks.length : 1;
    var postStatus = 0, postBody = '';

    if (chunkCount <= 1) {
      try {
        var resp = await arweave.api.post('tx', tx);
        postStatus = resp.status;
        postBody = typeof resp.data === 'string' ? resp.data : JSON.stringify(resp.data);
      } catch (e) {
        postStatus = (e && e.response && e.response.status) || -1;
        if (e && e.response && e.response.data) {
          postBody = String(e.response.data);
        } else {
          postBody = (e && e.message) || String(e);
        }
      }
      if (postStatus < 200 || postStatus >= 300) {
        throw new Error('节点拒收交易（' + postStatus + '）：' + postBody.slice(0, 300));
      }
    } else {
      // 多块交给 upload，它会把 /tx 与逐块 /chunk 都走完。
      // 这条路看不到响应体，但块多时自己实现风险更大。
      await arweave.transactions.upload(tx);
      postStatus = 200;
    }

    // 等一会儿再查一次状态。
    //
    // 不要刚 POST 完就查：节点是异步收录的，那一刻问往往得到 404，
    // 而这并不代表交易丢了。这里问不到也只记一笔，不当作失败：
    // 真正的确认要等区块，那是几分钟之后的事。
    await delay(3000);
    var st = null;
    try { st = await arweave.transactions.getStatus(tx.id); } catch (e) { st = null; }

    // 只回一个 ID 就够了，不必回传签名字段。
    // 其余字段都是给日志用的：「节点接受了但查不到」这类问题，
    // 只能靠 POST 的响应原话才能说清楚。
    var stStatus = 0;
    if (st && typeof st === 'object' && 'status' in st) { stStatus = st.status; }
    return JSON.stringify({
      id: tx.id,
      uploaded: true,
      status: stStatus,
      reward: tx.reward,
      postStatus: postStatus,
      postBody: String(postBody).slice(0, 500),
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
  var walletConnected = false;

  function stopSigning() { signStop = true; }

  async function connectWallet() {
    log('正在等待钱包扩展…');
    try {
      var w = await waitForWallet(30000);
      await w.connect(['ACCESS_ADDRESS', 'SIGNATURE', 'SIGN_TRANSACTION']);
      walletConnected = true;
      el('walletState').textContent = '已连接';
      el('walletState').className = 'ok';
      note('已连接钱包', 'ok');
      return true;
    } catch (e) {
      var msg = e && e.message ? e.message : String(e);
      walletConnected = false;
      el('walletState').textContent = '未连接';
      el('walletState').className = 'err';
      log(msg + '。请安装并启用 Wander。', 'err');
      return false;
    }
  }

  // runSignLoop 反复问 /sign/api/next，把待签内容交给钱包。
  async function runSignLoop() {
    signStop = false;

    // 没连过就先连。否则 signDataItem 会被钱包直接拒，
    // 而用户看到的现象是「一直没有弹窗」，很难猜到是没授权。
    if (!walletConnected) {
      var ok = await connectWallet();
      if (!ok) {
        note('钱包没连上，签名无法开始', 'err');
        return;
      }
    }

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
          ? await signAndUploadBundle(buf, task.tags)
          : await wallet().signDataItem({ data: new Uint8Array(buf), tags: task.tags });

        await api('/sign/api/sign/' + task.id, { method: 'POST', body: signed });
      } catch (e) {
        note('签名失败：' + (e && e.message ? e.message : String(e)), 'err');
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

    // 查重名要用当前磁盘上的路径，在这里更新，refresh 之后就准了。
    // 沿途的目录也算「已存在」：拖入一个同名的目录时要能发现。
    allFiles = st.files || [];
    currentPaths = {};
    allFiles.forEach(function (f) {
      currentPaths[f.path] = true;
      var parts = f.path.split('/');
      parts.pop();
      var acc = '';
      parts.forEach(function (p) {
        acc = acc ? acc + '/' + p : p;
        currentPaths[acc] = true;
      });
    });

    // 当前目录可能是刚被删掉的，那就退回根，不然会停在一个不存在的地方
    if (cwd && !currentPaths[cwd]) cwd = '';
    renderFiles();

    el('fileSummary').textContent = allFiles.length + ' 个文件 · ' + fmtSize(st.totalSize || 0);

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

  // ---- 站点文件：逐层浏览 ----
  //
  // 一次只列一个目录，像资源管理器。之前把整棵树铺开，
  // 文件一多就要在长列表里找，还不如一层层走。

  var cwd = '';        // 当前目录（相对站点根），'' 是根
  var allFiles = [];   // 最近一次拉到的扁平清单，用来算目录大小
  var clip = [];       // 站点内的剪贴板
  var selected = {};   // 被选中的路径

  // childrenOf 取出某个目录的直接子项。
  //
  // 扁平清单里每个路径都带全部层级，这里按「当前前缀」切一层出来。
  function childrenOf(files, dir) {
    var prefix = dir ? dir + '/' : '';
    var dirs = {};
    var out = [];

    files.forEach(function (f) {
      if (prefix && f.path.indexOf(prefix) !== 0) return;
      var rest = f.path.slice(prefix.length);
      if (!rest) return;
      var i = rest.indexOf('/');
      if (i < 0) {
        out.push({ type: 'file', name: rest, path: f.path, size: f.size, digest: f.digest });
      } else {
        dirs[rest.slice(0, i)] = true;
      }
    });

    var dirList = Object.keys(dirs).sort().map(function (d) {
      return { type: 'dir', name: d, path: prefix + d };
    });
    out.sort(function (a, b) { return a.name < b.name ? -1 : 1; });
    return dirList.concat(out);
  }

  // dirStats 数一个目录里有多少文件、共多大。
  function dirStats(dir) {
    var prefix = dir + '/';
    var n = 0, size = 0;
    allFiles.forEach(function (f) {
      if (f.path.indexOf(prefix) === 0) { n++; size += f.size; }
    });
    return { count: n, size: size };
  }

  function span(cls, text) {
    var s = document.createElement('span');
    if (cls) s.className = cls;
    if (text !== undefined) s.textContent = text;
    return s;
  }

  function enterDir(p) {
    cwd = p || '';
    selected = {};
    renderFiles();
  }

  function renderCrumbs() {
    var box = el('crumbs');
    box.textContent = '';

    var root = document.createElement('a');
    root.href = 'javascript:void(0)';
    root.textContent = '站点根';
    root.onclick = function () { enterDir(''); };
    box.appendChild(root);

    var acc = '';
    (cwd ? cwd.split('/') : []).forEach(function (part) {
      acc = acc ? acc + '/' + part : part;
      box.appendChild(span('muted', ' / '));
      var a = document.createElement('a');
      a.href = 'javascript:void(0)';
      a.textContent = part;
      var target = acc;
      a.onclick = function () { enterDir(target); };
      box.appendChild(a);
    });
  }

  // actionCell 给一行拼出操作按钮。
  //
  // 目录与文件一样要能删、能复制：这是「轻量资源管理器」与
  // 一张文件表的区别。
  function actionCell(item) {
    var act = span('tact');

    var copy = document.createElement('button');
    copy.textContent = '复制';
    copy.onclick = function (e) {
      e.stopPropagation();
      clip = [item.path];
      updateClipInfo();
      log('已记下 ' + item.path + '，切到目标目录后点粘贴', 'ok');
    };

    var del = document.createElement('button');
    del.textContent = '删除';
    del.onclick = function (e) {
      e.stopPropagation();
      removeEntry(item);
    };

    act.appendChild(copy);
    act.appendChild(del);
    return act;
  }

  function fileRow(item) {
    var row = document.createElement('div');
    row.className = 'trow';
    row.dataset.path = item.path;
    if (selected[item.path]) row.classList.add('sel');

    row.appendChild(span('twist', ''));
    row.appendChild(span('tname mono', item.name));
    row.appendChild(span('tsize', fmtSize(item.size)));
    row.appendChild(span('tdigest', (item.digest || '').slice(0, 8)));
    row.appendChild(actionCell(item));

    row.onclick = function (e) {
      if (e.target.tagName === 'BUTTON') return;
      toggleSelect(item.path, row);
    };
    return row;
  }

  function dirRow(item) {
    var row = document.createElement('div');
    row.className = 'trow dir';
    row.dataset.path = item.path;
    if (selected[item.path]) row.classList.add('sel');

    var st = dirStats(item.path);

    row.appendChild(span('twist', '▸'));
    row.appendChild(span('tname dirname mono', item.name + '/'));
    row.appendChild(span('tsize', fmtSize(st.size)));
    row.appendChild(span('tdigest', st.count + ' 个'));
    row.appendChild(actionCell(item));

    row.onclick = function (e) {
      if (e.target.tagName === 'BUTTON') return;
      enterDir(item.path);
    };
    return row;
  }

  function upRow() {
    var row = document.createElement('div');
    row.className = 'trow up';
    row.appendChild(span('twist', '↑'));
    row.appendChild(span('tname mono', '..'));
    row.appendChild(span('tsize', ''));
    row.appendChild(span('tdigest', ''));
    row.appendChild(span('tact', ''));
    row.onclick = function () {
      var parts = cwd.split('/');
      parts.pop();
      enterDir(parts.join('/'));
    };
    return row;
  }

  function toggleSelect(p, row) {
    if (selected[p]) {
      delete selected[p];
      row.classList.remove('sel');
    } else {
      selected[p] = true;
      row.classList.add('sel');
    }
    updateClipInfo();
  }

  function updateClipInfo() {
    var n = Object.keys(selected).length;
    el('clipInfo').textContent = clip.length
      ? ('已复制 ' + clip.length + ' 项' + (n ? '，选中 ' + n + ' 项' : ''))
      : (n ? '选中 ' + n + ' 项' : '');
  }

  // renderFiles 重画当前目录。不拉 state：进出目录不改变磁盘。
  function renderFiles() {
    renderCrumbs();
    var box = el('files');
    box.textContent = '';

    if (cwd) box.appendChild(upRow());

    var kids = childrenOf(allFiles, cwd);
    if (!kids.length) {
      var empty = document.createElement('div');
      empty.className = 'trow';
      empty.appendChild(span('twist', ''));
      empty.appendChild(span('tname muted', cwd ? '这个目录是空的，把文件拖进来。' : '站点里还没有文件。'));
      box.appendChild(empty);
    } else {
      kids.forEach(function (k) {
        box.appendChild(k.type === 'dir' ? dirRow(k) : fileRow(k));
      });
    }

    el('fileUp').disabled = !cwd;
    updateClipInfo();
  }

  // ---- 文件操作 ----

  // removeEntry 删一个文件或整个目录。
  // 删除不可撤销，所以先说清楚要删什么，目录还要点名「及其中的全部内容」。
  async function removeEntry(item) {
    var what = item.type === 'dir'
      ? '目录 ' + item.path + ' 及其中的全部内容'
      : item.path;
    if (!confirm('删除 ' + what + '？\n\n这个动作不能撤销。')) return;

    var res = await api('/api/files/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, path: item.path }),
    });
    var out = await res.json();
    if (!res.ok) { log('删除失败：' + (out.error || res.status), 'err'); return; }
    log('已删除 ' + item.path, 'ok');

    // 删掉的正是当前目录或它的祖先时，退到根，不然会停在一个不存在的地方
    if (cwd === item.path || cwd.indexOf(item.path + '/') === 0) cwd = '';
    refresh();
  }

  function copySelected() {
    var picks = Object.keys(selected);
    if (!picks.length) { log('先点一行选中它', 'warn'); return; }
    clip = picks.slice();
    updateClipInfo();
    log('已记下 ' + clip.length + ' 项，切到目标目录后点粘贴', 'ok');
  }

  // pasteClip 把剪贴板里的东西复制到当前目录。
  //
  // 同名时先问一次，与拖入同一套规矩：重不重名是拖入/粘贴时
  // 真正要判断的事，而不是「落在哪一行上」。
  async function pasteClip() {
    if (!clip.length) { log('剪贴板是空的：先点某一行的「复制」', 'warn'); return; }

    var targets = clip.map(function (p) {
      var name = p.split('/').pop();
      return cwd ? cwd + '/' + name : name;
    });
    var clashes = targets.filter(function (t) { return currentPaths[t]; });
    if (clashes.length) {
      if (!confirm('目标目录里已有 ' + clashes.length + ' 个同名项：' + clashes.slice(0, 3).join('、') +
        '\n\n确定 = 覆盖它们；取消 = 放弃这次粘贴。')) {
        log('已取消粘贴', 'warn');
        return;
      }
    }

    var res = await api('/api/files/copy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, from: clip, to: cwd }),
    });
    var out = await res.json();
    if (!res.ok) { log('粘贴失败：' + (out.error || res.status), 'err'); return; }

    var ok = 0, bad = [];
    (out.results || []).forEach(function (r) {
      if (r.error) bad.push(r.path + '（' + r.error + '）'); else ok++;
    });
    if (ok) log('已粘贴 ' + ok + ' 项', 'ok');
    if (bad.length) log('有 ' + bad.length + ' 项没成：\n  ' + bad.join('\n  '), 'err');
    refresh();
  }

  async function newFolder() {
    var name = prompt('新文件夹的名字');
    if (name === null) return;
    name = name.trim();
    if (!name) return;
    if (name.indexOf('/') >= 0 || name.indexOf('\\') >= 0) {
      log('名字里不能带路径分隔符', 'err');
      return;
    }
    var p = cwd ? cwd + '/' + name : name;

    var res = await api('/api/files/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, path: p }),
    });
    var out = await res.json();
    if (!res.ok) { log('新建失败：' + (out.error || res.status), 'err'); return; }
    log('已新建 ' + p, 'ok');
    refresh();
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

  // attachPanelDrop 把一块区域变成拖放区，落点由 baseFn() 给出。
  //
  // 落点不再是「拖到哪一行上」：那是上一版的思路，很反直觉。
  // 现在整个面板就是一个落点，拖进来之后要判断的是「重不重名」，
  // 而那件事与拖到哪个位置无关。
  function attachPanelDrop(node, baseFn) {
    node.addEventListener('dragover', function (e) {
      e.preventDefault();
      node.classList.add('drop');
    });
    node.addEventListener('dragleave', function () { node.classList.remove('drop'); });
    node.addEventListener('drop', function (e) {
      e.preventDefault();
      node.classList.remove('drop');
      dropInto(baseFn(), e.dataTransfer);
    });
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
      runTask(lastTask.url, lastTask.body, lastTask.label, lastTask.opts);
    };
    el('logs').appendChild(btn);
  }

  // 任务跑着的时候把触发按钮都禁掉。
  //
  // 打包与发布都不该并发跑：连点两下就是对着同一个目录各干一遍，
  // 而且两边都以为自己在改同一份东西。后端也有自己的锁，
  // 但让按钮当场变灰更直接——用户不用等到报错才知道已经在跑了。
  var BUSY_BUTTONS = ['doPack', 'doPublish', 'doSiteInit'];

  function setBusy(busy) {
    BUSY_BUTTONS.forEach(function (id) {
      var b = el(id);
      if (b) b.disabled = busy;
    });
  }

  // 所有耗时操作都走这里：先拿 taskId，再订阅 SSE 看进度。
  //
  // opts.sign 为真时同时跑签名循环。这件事必须挂在这里而不是绑在
  // 「发布」按钮上：重试走的是同一个 runTask，绑在按钮上就会漏掉，
  // 而后端一直在等签名，用户只看到一句「等待钱包确认」却没有任何反应。
  async function runTask(url, body, label, opts) {
    clearLog();
    log('> ' + label);
    var o = opts || {};
    lastTask = { url: url, body: body, label: label, opts: o };
    setBusy(true);

    if (o.sign) {
      // 不 await：它要一直跑到任务收尾
      runSignLoop();
    }

    var res = await api(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    var out = await res.json();
    if (!res.ok) {
      log('发起失败：' + (out.error || res.status), 'err');
      setBusy(false);
      return;
    }
    if (!out.taskId) {
      log('后端没有返回 taskId', 'err');
      setBusy(false);
      return;
    }
    currentTaskId = out.taskId;

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
      setBusy(false);
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
    src.onerror = function () { src.close(); setBusy(false); };
  }

  el('refresh').onclick = refresh;
  el('connWallet').onclick = connectWallet;

  el('fileUp').onclick = function () {
    var parts = cwd ? cwd.split('/') : [];
    parts.pop();
    enterDir(parts.join('/'));
  };
  el('fileCopy').onclick = copySelected;
  el('filePaste').onclick = pasteClip;
  el('fileMkdir').onclick = newFolder;

  // 整个面板就是一个拖放区，落点永远是当前目录
  attachPanelDrop(el('files'), function () { return cwd; });

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
      proxy: el('packProxy').value.trim(),
      rebuild: el('packRebuild').checked,
    }, '打包');
  };

  el('doPublish').onclick = function () {
    runTask('/api/publish', {
      site: site,
      target: activeTarget,
      dest: el('pubDest').value.trim(),
      endpoint: el('pubEndpoint').value.trim(),
      node: el('pubNode').value.trim(),
      from: el('pubFrom').value.trim(),
      proxyMode: el('pubProxyMode').value,
      proxyUrl: el('pubProxyUrl').value.trim(),
    }, '发布到 ' + activeTarget, {
      // 两条 Arweave 路都要钱包签名；本地目录不需要。
      // 这件事交给 runTask 办，重试时才能一起带上。
      sign: activeTarget !== 'local',
    });
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
