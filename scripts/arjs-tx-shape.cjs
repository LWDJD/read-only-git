// 打印 arweave-js 的 Transaction.toJSON() 到底是什么形状。
//
// 为什么要有它：Go 侧拼交易 JSON 时，字段名与取值口径必须与 arweave-js
// 完全一致，而「一致」不能靠读文档猜。这个脚本把它的真实输出打出来，
// 是 internal/arweave/txpayload_test.go 里那组期望值的出处。
//
// 最反直觉的一条是 tags：交易 JSON 里的 name/value 是 base64url，不是明文。
// 因为 addTag 内部先编码再存，而 toJSON() 原样输出这份内部表示。
// 之前 Go 侧发明文，节点回的就是 400 Invalid JSON。
//
// 用法：
//   ARWEAVE_JS_PATH=<包目录> node scripts/arjs-tx-shape.cjs
//
// 跑之前那份包目录要 npm install --omit=dev（否则缺 bignumber.js）。
//
// 不走 arweave.createTransaction：它会联网取价格与 last_tx，
// 在没网或连不通的环境里直接失败。这里只构造 Transaction 再本地算 chunks。

const path = require('path');

const pkg = process.env.ARWEAVE_JS_PATH;
if (!pkg) {
  console.error('需要 ARWEAVE_JS_PATH=<arweave-js 包目录>');
  process.exit(1);
}

const txMod = require(path.join(pkg, 'node', 'lib', 'transaction.js'));
const Transaction = txMod.default || txMod.Transaction;

(async () => {
  const data = new Uint8Array([104, 105]); // "hi"
  const tx = new Transaction({ data });
  tx.addTag('App-Name', 'read-only-git');
  tx.addTag('Content-Type', 'text/html; charset=utf-8');
  tx.addTag('Path', 'index.html');

  await tx.prepareChunks(data);
  tx.data_size = data.byteLength.toString();

  const j = tx.toJSON();
  console.log('字段顺序 :', Object.keys(j).join(', '));
  console.log('tags     :', JSON.stringify(j.tags));
  console.log('data     :', JSON.stringify(j.data));
  console.log('data_size:', JSON.stringify(j.data_size));
  console.log('data_root:', JSON.stringify(j.data_root));
  console.log('data_tree:', JSON.stringify(j.data_tree));
  console.log('format   :', JSON.stringify(j.format));
  console.log('quantity :', JSON.stringify(j.quantity));
  console.log('reward   :', JSON.stringify(j.reward));
  console.log('target   :', JSON.stringify(j.target));
  console.log('last_tx  :', JSON.stringify(j.last_tx));
})().catch((e) => {
  console.error('ERR', e && e.message ? e.message : e);
  process.exit(1);
});
