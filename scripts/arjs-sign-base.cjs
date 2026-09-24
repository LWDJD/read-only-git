// 用 arweave-js 签一笔 format-2 交易，把 toJSON() 落盘当作基准。
//
// 为什么要有它：Go 侧拼交易 JSON 的字段与编码必须与 arweave-js 完全一致，
// 而「一致」不能靠读文档猜。这份输出喂给 txpayload_test.go 里的对拍用例。
//
// 一个具体的教训：曾经提交交易被节点回 400 Invalid JSON，
// 原因是 tags 没按 base64url 编码；后来改成 verification failed，
// 查下来拼法本身没问题。两次都靠这类对拍才定案。
//
// 用法：
//   ARWEAVE_JS_PATH=<包目录> node scripts/arjs-sign-base.cjs [输出文件]
//
// 全程不联网：绕开 createTransaction（它会取价格与 last_tx），字段自己给。
// 跑之前那份包目录要 npm install --omit=dev。

const path = require('path');
const fs = require('fs');

const pkg = process.env.ARWEAVE_JS_PATH;
if (!pkg) {
  console.error('需要 ARWEAVE_JS_PATH=<arweave-js 包目录>');
  process.exit(1);
}

const outFile = process.argv[2] || 'arjs-signed.json';

const Arweave = require(path.join(pkg, 'node', 'index.js'));
const txMod = require(path.join(pkg, 'node', 'lib', 'transaction.js'));
const Transaction = txMod.default || txMod.Transaction;

(async () => {
  const arweave = Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });

  const data = new Uint8Array([104, 105, 106]); // "hij"
  const tx = new Transaction({ data });
  tx.addTag('App-Name', 'read-only-git');
  tx.addTag('Repo', 'demo');
  tx.addTag('Path', 'index.html');

  await tx.prepareChunks(data);
  tx.data_size = data.byteLength.toString();
  tx.reward = '0';
  // 真链上 last_tx 来自网络的 anchor，这里给一个固定值充当那一份
  tx.last_tx = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';

  const jwk = await arweave.wallets.generate();
  await arweave.transactions.sign(tx, jwk);

  const j = tx.toJSON();
  fs.writeFileSync(outFile, JSON.stringify(j, null, 2));

  console.log('字段     :', Object.keys(j).join(', '));
  console.log('tags     :', JSON.stringify(j.tags));
  console.log('data     :', JSON.stringify(j.data));
  console.log('data_size:', JSON.stringify(j.data_size));
  console.log('data_root:', JSON.stringify(j.data_root));
  console.log('已写入   :', outFile);

  // 往返一遍：从 JSON 重建后，签名输入应当逐字节不变
  const rebuilt = new Transaction(JSON.parse(JSON.stringify(j)));
  const a = await tx.getSignatureData();
  const b = await rebuilt.getSignatureData();
  console.log('往返一致 :', Buffer.compare(Buffer.from(a), Buffer.from(b)) === 0);
})().catch((e) => {
  console.error('ERR', (e && e.stack) || e);
  process.exit(1);
});
