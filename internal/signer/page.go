package signer

// DefaultPage 是内置的签名页。
//
// 它的职责很窄：连钱包、按 /api/next 的指引逐个签名、把结果送回去。
// 不在页面侧做任何判断，所有决策都留在 Go 里，页面只是一个通道。
const DefaultPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>read-only-git · 签名</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif;
         max-width: 640px; margin: 48px auto; padding: 0 20px; }
  h1 { font-size: 20px; margin: 0 0 4px; }
  .muted { opacity: .65; }
  #status { margin: 20px 0; padding: 14px 16px; border-radius: 8px;
            background: rgba(128,128,128,.12); }
  ol { padding-left: 22px; opacity: .75; font-size: 13px; }
  button { font: inherit; padding: 8px 16px; border-radius: 6px; cursor: pointer; }
</style>
</head>
<body>
<h1>read-only-git</h1>
<p class="muted">把待上链的内容交给钱包签名</p>
<p id="status">准备中…</p>
<button id="go" hidden>开始签名</button>
<ol id="log"></ol>

<script src="/vendor/arweave.js"></script>
<script>
(function () {
  var statusEl = document.getElementById('status');
  var logEl = document.getElementById('log');
  var goEl = document.getElementById('go');

  // token 从地址栏取，所有请求都带上。服务只绑本机，
  // 但同机的任意网页都能向它发请求，靠这一层挡住别的页面。
  var TOKEN = new URLSearchParams(location.search).get('token') || '';

  function api(path, opts) {
    var o = Object.assign({}, opts || {});
    o.headers = Object.assign({}, o.headers || {}, { 'X-Rog-Token': TOKEN });
    return fetch(path, o);
  }

  function say(text) { statusEl.textContent = text; }

  function addLine(text) {
    var li = document.createElement('li');
    li.textContent = text;
    logEl.appendChild(li);
  }

  function wallet() { return window.arweaveWallet; }

  // 价格交给 arweave-js 去问节点：不设 reward，它就会调
  // /price/<这一包的字节数>（见 arweave-js common.js 的 createTransaction）。
  //
  // 这里曾经写过乘 2。那是排查「交易上了链、网关却打不开」时
  // 为了排除变量加的，真因最后落在 bundle 缺少 ANS-104 头部
  // （见 arweave/bundle.go），与手续费无关。
  //
  // 按节点报价付就够：节点校验 is_tx_fee_sufficient 要求 reward >=
  // get_tx_fee(...)，而 /price 返回的就是 get_tx_fee 的结果，两者同源。
  // ArDrive 的做法也是这个：它的 FeeMultiple 默认值就是 1.0。
  //
  // 已知例外：从未发过交易的地址要多付一笔 NewAccountFee
  // （ar_tx:get_tx_fee2 里的那个分支），/price/<size> 不含它。

  /**
   * 让钱包签一笔「data 就是这一整包」的交易，然后自己提交上链。
   *
   * 为什么提交也放在这里：署名用的对象与提交出去的对象是同一个，
   * 就不存在「两处各自拼出来的交易 JSON 是否等价」这个问题。
   * 中间那一次 verify 也是这个用意：先把「签名本身对不对」
   * 与「提交环节对不对」分开，出错时才能知道是哪一头。
   */
  async function signAndUploadBundle(buf, tags) {
    if (!window.Arweave) throw new Error('arweave-js 没加载出来');
    var arweave = window.Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });
    var tx = await arweave.createTransaction({ data: new Uint8Array(buf) });
    var list = tags || [];
    for (var i = 0; i < list.length; i++) {
      tx.addTag(list[i].name, list[i].value);
    }
    // 不设 reward，createTransaction 会去问 /price/<字节数>，照报价付。
    // 省略 JWK 参数时 arweave-js 会走注入的钱包
    await arweave.transactions.sign(tx);

    // 自验：签名与签名输入对不对得上。不过就说明钱包给的东西有问题。
    var ok = false;
    try { ok = await arweave.transactions.verify(tx); } catch (e) { ok = false; }
    if (!ok) {
      throw new Error('签名自验没过：钱包返回的 owner / signature 与这笔交易的签名输入对不上');
    }

    // 提交。
    //
    // 单块：自己 POST，能看见节点回的每一句话。
    // 多块：不在这里提交，把 proofs 回传给 Go，由它走 /tx → 逐块 /chunk。
    //
    // 为什么多块不自己上：arweave-js 的 upload() 不暴露响应体，
    // 实测栽过的正是「upload 说成功、链上却查不到」。
    // Go 那条路每一步都写日志、失败会退避重试、致命错会单独挑出来。
    var chunkCount = (tx.chunks && tx.chunks.chunks) ? tx.chunks.chunks.length : 1;

    if (chunkCount <= 1) {
      var postStatus = 0, postBody = '';
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

      // 等一会儿再查状态：节点是异步收录的，立刻问往往得到 404。
      await delay(3000);
      var st = null;
      try { st = await arweave.transactions.getStatus(tx.id); } catch (e) { st = null; }
      var stStatus = 0;
      if (st && typeof st === 'object' && 'status' in st) { stStatus = st.status; }

      return JSON.stringify({
        id: tx.id,
        uploaded: true,
        chunkCount: 1,
        status: stStatus,
        reward: tx.reward,
        postStatus: postStatus,
        postBody: String(postBody).slice(0, 500),
      });
    }

    var proofs = [];
    var prfs = (tx.chunks && tx.chunks.proofs) ? tx.chunks.proofs : [];
    for (var pi = 0; pi < prfs.length; pi++) {
      proofs.push({
        data_path: arweave.utils.bufferTob64Url(prfs[pi].proof),
        offset: String(prfs[pi].offset),
      });
    }
    if (proofs.length === 0) {
      throw new Error('这一包需要分块，却没拿到分块证明，没法交给 Go 提交');
    }
    return JSON.stringify({
      id: tx.id,
      uploaded: false,
      chunkCount: chunkCount,
      owner: tx.owner,
      signature: tx.signature,
      reward: tx.reward,
      last_tx: tx.last_tx,
      data_root: tx.data_root,
      data_size: String(tx.data_size),
      // tags 原样回传（签名时交易里的那份）：签名输入里的 tags 就是它，
      // 提交出去的也必须是它。Go 另拿明文编码一份的话，
      // 节点解码出的字节与签名输入不同，会被拒 Transaction verification failed。
      tags: tx.tags,
      proofs: proofs,
    });
  }

  /**
   * base64url 编码，不带填充。
   *
   * 优先用 arweave-js 的；取不到就自己编一份，
   * 免得因为一个工具函数让整条分块路径在某个版本上失效。
   */
  function toB64Url(bytes) {
    var u = window.Arweave && window.Arweave.utils;
    if (u && typeof u.bufferTob64Url === 'function') {
      return u.bufferTob64Url(bytes);
    }
    var bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function delay(ms) {
    return new Promise(function (r) { setTimeout(r, ms); });
  }

  function describe(task) {
    if (task.tags) {
      for (var i = 0; i < task.tags.length; i++) {
        if (task.tags[i].name === 'Path') return task.tags[i].value;
      }
    }
    return task.id;
  }

  async function run() {
    goEl.hidden = true;
    var n = 0;
    var disconnects = 0;

    for (;;) {
      var task = null;
      try {
        var res = await api('/api/next', { cache: 'no-store' });
        task = await res.json();
      } catch (e) {
        // 连不上后端。很可能它已经收工并把服务关了，这不是错误，
        // 所以重试几次再判定，避免把正常收尾报成吓人的红字。
        disconnects += 1;
        if (disconnects >= 3) {
          if (n > 0) {
            say('已完成，共签名 ' + n + ' 个。可以关闭本页了。');
          } else {
            say('与后端的连接已断开，没有收到任务。');
          }
          return;
        }
        await delay(400);
        continue;
      }
      disconnects = 0;

      if (!task || !task.id) {
        // 暂时没有待签任务，而不是结束了：后端是串行提交的，
        // 中间会有空窗。稍后再看。
        await delay(300);
        continue;
      }

      try {
        var res = await api('/api/blob/' + task.id);
        if (!res.ok) throw new Error('取内容失败：' + res.status);
        var buf = await res.arrayBuffer();
        // 两类任务的产物不同：
        //   dataitem 回传签名字节
        //   tx       回传交易的签名字段
        // 另外，钱包的 signDataItem 只接受 string 或 Uint8Array，
        // 直接递 ArrayBuffer 会被它内部的断言挡下。
        var signed = task.kind === 'tx'
          ? await signAndUploadBundle(buf, task.tags)
          : await wallet().signDataItem({ data: new Uint8Array(buf), tags: task.tags });
        await api('/api/sign/' + task.id, { method: 'POST', body: signed });
      } catch (e) {
        say('签名失败（' + describe(task) + '）：' + (e && e.message ? e.message : String(e)));
        return;
      }

      n += 1;
      addLine('已签名 ' + describe(task));
      say('已签名 ' + n + ' 个');
    }
  }

  // 钱包扩展是异步注入 window.arweaveWallet 的，可能晚于本脚本执行，
  // 所以不能只检查一次就下结论。轮询等待一段时间再放弃。
  function waitForWallet(ms) {
    return new Promise(function (resolve, reject) {
      var deadline = Date.now() + ms;
      (function poll() {
        if (wallet()) return resolve(wallet());
        if (Date.now() > deadline) {
          return reject(new Error('没检测到 Arweave 钱包扩展'));
        }
        setTimeout(poll, 150);
      })();
    });
  }

  async function init() {
    say('正在等待钱包扩展…');
    try {
      var w = await waitForWallet(30000);
      await w.connect(['ACCESS_ADDRESS', 'SIGNATURE', 'SIGN_TRANSACTION']);
      say('已连接钱包，点下面的按钮开始');
      goEl.hidden = false;
    } catch (e) {
      var msg = e && e.message ? e.message : String(e);
      say(msg + '。请安装并启用 Wander 后刷新本页。');
    }
  }

  goEl.addEventListener('click', function () {
    run().catch(function (e) {
      say('出错：' + (e && e.message ? e.message : String(e)));
    });
  });

  init();
})();
</script>
</body>
</html>
`
