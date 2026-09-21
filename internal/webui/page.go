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
  a { color: var(--link); }
  h2 { display: flex; align-items: center; justify-content: space-between; }
</style>
</head>
<body>
<header>
  <h1>read-only-git 维护台</h1>
  <span class="muted" id="sitePath">…</span>
  <button id="refresh" style="margin:0 0 0 auto">重新扫描</button>
</header>

<main>
  <div>
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
      <label>仓库名（Repo 标签 / 记录身份）</label>
      <input id="pubRepo" placeholder="myrepo">
      <label>从链上恢复（可选，填入口 id）</label>
      <input id="pubFrom" placeholder="re22tX-…">
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
      <p class="muted" style="margin:0 0 8px">把文件拖到下面任意一行上，即可替换该路径的内容。</p>
      <table>
        <thead><tr><th>路径</th><th style="width:90px">大小</th><th style="width:110px">sha256</th><th style="width:120px"></th></tr></thead>
        <tbody id="files"></tbody>
      </table>
    </section>
  </div>
</main>

<div class="logbox" id="logs">task 日志会显示在这里。</div>

<script>
(function () {
  var site = '';
  var activeTarget = 'turbo';

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

  // 拉一次状态并重画。任何写操作之后都要调用它，不做乐观更新。
  async function refresh() {
    var res = await fetch('/api/state');
    var st = await res.json();

    site = st.site;
    el('sitePath').textContent = st.site;

    if (st.error) {
      log(st.error, 'err');
    }

    var tb = el('files');
    tb.textContent = '';
    (st.files || []).forEach(function (f) {
      var tr = document.createElement('tr');
      tr.dataset.path = f.path;

      var td1 = document.createElement('td');
      td1.className = 'mono';
      td1.textContent = f.path;

      var td2 = document.createElement('td');
      td2.textContent = fmtSize(f.size);

      var td3 = document.createElement('td');
      td3.className = 'mono';
      td3.textContent = (f.digest || '').slice(0, 8);

      var td4 = document.createElement('td');
      var del = document.createElement('button');
      del.textContent = '删除';
      del.style.marginTop = '0';
      del.onclick = function () { removeFile(f.path); };
      td4.appendChild(del);

      tr.appendChild(td1); tr.appendChild(td2); tr.appendChild(td3); tr.appendChild(td4);
      attachDrop(tr);
      tb.appendChild(tr);
    });
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

  // 拖拽替换：读成字节，base64 后交给后端。
  function attachDrop(tr) {
    tr.addEventListener('dragover', function (e) {
      e.preventDefault();
      tr.classList.add('drop');
    });
    tr.addEventListener('dragleave', function () { tr.classList.remove('drop'); });
    tr.addEventListener('drop', async function (e) {
      e.preventDefault();
      tr.classList.remove('drop');
      var file = e.dataTransfer.files && e.dataTransfer.files[0];
      if (!file) return;

      var buf = await file.arrayBuffer();
      var bytes = new Uint8Array(buf);
      var bin = '';
      for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);

      var res = await fetch('/api/files/replace', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ site: site, path: tr.dataset.path, bytes: btoa(bin) }),
      });
      var out = await res.json();
      if (!res.ok) { log('替换失败：' + (out.error || res.status), 'err'); return; }
      log('已替换 ' + tr.dataset.path + '（' + fmtSize(out.size) + '）', 'ok');
      refresh();
    });
  }

  async function removeFile(path) {
    var res = await fetch('/api/files/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ site: site, path: path }),
    });
    var out = await res.json();
    if (!res.ok) { log('删除失败：' + (out.error || res.status), 'err'); return; }
    log('已删除 ' + path, 'ok');
    refresh();
  }

  // 所有耗时操作都走这里：先拿 taskId，再订阅 SSE 看进度。
  async function runTask(url, body, label) {
    clearLog();
    log('> ' + label);

    var res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    var out = await res.json();
    if (!res.ok) { log('发起失败：' + (out.error || res.status), 'err'); return; }
    if (!out.taskId) { log('后端没有返回 taskId', 'err'); return; }

    var src = new EventSource('/api/task/' + out.taskId + '/events');
    var seen = 0;

    src.onmessage = function (ev) {
      log(ev.data);
      seen++;
    };
    src.addEventListener('end', function () {
      src.close();
      // 结束后拉一次状态与任务结果，顺便把签名页地址这类附加信息取出来
      fetch('/api/task/' + out.taskId)
        .then(function (r) { return r.json(); })
        .then(function (t) {
          if (t.data && t.data.signUrl) {
            log('签名页 ' + t.data.signUrl, 'ok');
            var a = document.createElement('a');
            a.href = t.data.signUrl;
            a.target = '_blank';
            a.textContent = '打开签名页';
            el('logs').appendChild(a);
          }
          if (t.status === 'failed') log('失败：' + (t.error || '未知错误'), 'err');
          else log('完成', 'ok');
          refresh();
        });
    });
    src.onerror = function () { src.close(); };
  }

  el('refresh').onclick = refresh;

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

  el('doPack').onclick = function () {
    runTask('/api/pack', {
      source: el('packSource').value.trim(),
      outDir: el('packOut').value.trim(),
      name: el('packName').value.trim(),
      incremental: el('packIncremental').checked,
    }, '打包');
  };

  el('doPublish').onclick = function () {
    runTask('/api/publish', {
      site: site,
      target: activeTarget,
      dest: el('pubDest').value.trim(),
      endpoint: el('pubEndpoint').value.trim(),
      node: el('pubNode').value.trim(),
      repo: el('pubRepo').value.trim(),
      from: el('pubFrom').value.trim(),
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
