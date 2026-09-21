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

  function say(text) { statusEl.textContent = text; }

  function addLine(text) {
    var li = document.createElement('li');
    li.textContent = text;
    logEl.appendChild(li);
  }

  function wallet() { return window.arweaveWallet; }

  /**
   * 让钱包给一笔「data 就是这一整包」的交易签名。
   *
   * arweave-js 在这里只做两件事：构造交易（算 data_root、reward、last_tx），
   * 以及把交易交给钱包签。签完只回传字段，包体不动，
   * 免得整份内容再多走一趟 base64。
   */
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
    return JSON.stringify({
      id: tx.id,
      owner: tx.owner,
      signature: tx.signature,
      reward: tx.reward,
      last_tx: tx.last_tx,
      // data_root 是签名内容的一部分，Go 侧要拿它拼交易 JSON。
      // 交易 JSON 里漏了这个字段，签名就不再自洽，节点会拒。
      data_root: tx.data_root,
    });
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
        var res = await fetch('/api/next', { cache: 'no-store' });
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
        var res = await fetch('/api/blob/' + task.id);
        if (!res.ok) throw new Error('取内容失败：' + res.status);
        var buf = await res.arrayBuffer();
        // 两类任务的产物不同：
        //   dataitem 回传签名字节
        //   tx       回传交易的签名字段
        // 另外，钱包的 signDataItem 只接受 string 或 Uint8Array，
        // 直接递 ArrayBuffer 会被它内部的断言挡下。
        var signed = task.kind === 'tx'
          ? await signBundleTransaction(buf, task.tags)
          : await wallet().signDataItem({ data: new Uint8Array(buf), tags: task.tags });
        await fetch('/api/sign/' + task.id, { method: 'POST', body: signed });
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
