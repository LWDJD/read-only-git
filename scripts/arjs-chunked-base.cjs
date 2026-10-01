// 用 arweave-js 造一笔【多块】交易，把节点实际收到的 /tx body 落成基准。
//
// 为什么单块基准不够：单块走的是页面自己 POST（toJSON 带 data），
// 多块是「上传器把 data 置空后另拷一个 Transaction 再序列化」。
// 这条路的 /tx body 形状此前从未与 Go 的 txPayload 对拍过，
// 而真链上报的正是这条路的 400 Transaction verification failed。
//
// 它模拟的就是 transaction-uploader.js 的 postTransaction 多块分支：
//   new Transaction({ ...signed, data: new Uint8Array(0) }) → JSON.stringify
// 以及签名页回传给 Go 的 TxSignature JSON（signAndUploadBundle 的返回形态）。
//
// 用法：
//   ARWEAVE_JS_PATH=<包目录> node scripts/arjs-chunked-base.cjs [输出文件]
//
// 全程不联网：reward / last_tx / owner 都自己给固定值，
// 关键是形状与编码，不是值本身。

const path = require('path');
const fs = require('fs');

const pkg = process.env.ARWEAVE_JS_PATH;
if (!pkg) {
  console.error('需要 ARWEAVE_JS_PATH=<arweave-js 包目录>');
  process.exit(1);
}

const outFile = process.argv[2] || 'arjs-chunked-signed.json';

const Arweave = require(path.join(pkg, 'node', 'index.js'));
const txMod = require(path.join(pkg, 'node', 'lib', 'transaction.js'));
const Transaction = txMod.default || txMod.Transaction;

(async () => {
  const arweave = Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });

  // 300000 字节切成 2 块（与 arjs-vectors 的实测边界一致），
  // 填充规则同 crosscheck：data[i] = i & 0xff。
  const data = new Uint8Array(300000);
  for (let i = 0; i < data.length; i++) data[i] = i & 0xff;

  const tx = new Transaction({ data });
  tx.data_size = data.byteLength.toString();
  // 外层 bundle 的 tags（明文进 addTag，内部编码后存）
  tx.addTag('App-Name', 'read-only-git');
  tx.addTag('Content-Type', 'application/octet-stream');
  tx.addTag('Bundle-Format', 'binary');
  tx.addTag('Bundle-Version', '2.0.0');
  tx.addTag('Repo', 'demo');
  // 真链上这两个来自网络（getTransactionAnchor / getPrice），这里给固定值
  tx.reward = '39146849293';
  tx.last_tx = 'oEDkKJP0g8ySsmCAfzOT2pemAzQjx4xmFBNH50e_0PGDJOajSQhaZL2bozLWFqyB';

  await tx.prepareChunks(data);
  if (!tx.data_root) {
    throw new Error('prepareChunks 没算出 data_root');
  }

  const jwk = await arweave.wallets.generate();
  await arweave.transactions.sign(tx, jwk);

  // 自验：签名与签名输入对得上，基准才是「一笔好交易」
  if (!(await arweave.transactions.verify(tx))) {
    throw new Error('签名自验没过，基准无效');
  }

  // ---- 一、节点收到的 /tx body（多块） ----
  // 照 transaction-uploader.js 的构造器：拷一份、data 置空再序列化
  const zeroed = new Transaction(Object.assign({}, tx, { data: new Uint8Array(0) }));
  const body = JSON.parse(JSON.stringify(zeroed));
  // 真机上数据这么大时 body 里 data 为空串（置空后 bufferTob64Url(empty)）
  if (body.data !== '') {
    throw new Error('多块 body 的 data 该是空串，实际 ' + JSON.stringify(body.data));
  }

  // ---- 二、签名页回传给 Go 的 TxSignature JSON ----
  const proofs = tx.chunks.proofs.map((p) => ({
    data_path: Arweave.utils.bufferTob64Url(p.proof),
    offset: String(p.offset),
  }));
  const sig = {
    id: tx.id,
    uploaded: false,
    chunkCount: tx.chunks.chunks.length,
    owner: tx.owner,
    signature: tx.signature,
    reward: tx.reward,
    last_tx: tx.last_tx,
    data_root: tx.data_root,
    data_size: String(tx.data_size),
    tags: tx.tags,
    proofs: proofs,
  };

  // ---- 三、签名输入九项的深哈希（对不上时用它定位是哪一项） ----
  const sigData = await tx.getSignatureData();

  const out = {
    generatedBy: 'scripts/arjs-chunked-base.cjs (arweave-js ' + require(path.join(pkg, 'package.json')).version + ')',
    fill: 'data[i] = i & 0xff',
    size: data.length,
    txBody: body,
    pageSig: sig,
    signatureDataHex: Buffer.from(sigData).toString('hex'),
  };
  fs.writeFileSync(outFile, JSON.stringify(out, null, 2));

  console.log('body 键序 :', Object.keys(body).join(', '));
  console.log('data_size :', JSON.stringify(body.data_size), typeof body.data_size);
  console.log('data      :', JSON.stringify(body.data));
  console.log('data_tree :', JSON.stringify(body.data_tree));
  console.log('tags      :', JSON.stringify(body.tags));
  console.log('块数      :', sig.chunkCount);
  console.log('已写入    :', outFile);
})().catch((e) => {
  console.error('ERR', (e && e.stack) || e);
  process.exit(1);
});
