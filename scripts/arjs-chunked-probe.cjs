// 多块 L1 提交的验证失败探针：拿测试密钥造一笔注定被拒的多块交易，
// POST 给真节点，再 GET /tx/<id>/status 读回确切的错误码。
//
// 为什么值得跑：节点对 POST /tx 只回一句 "Transaction verification failed."
// （十项检查的统一出口），但被拒后它把【全部失败项的错误码】存了下来，
// GET /tx/<id>/status 会以 410 把它们吐出来。被拒的交易不进 mempool、
// 不花一分钱，却能一锤定音：签名链路有没有问题、还是费用/余额侧的问题。
//
// 预期（测试钱包余额为 0）：overspend，可能还有 tx_too_cheap。
// 一旦出现 tx_signature_not_valid / tx_id_not_valid / tx_data_size_data_root_mismatch
// 之类，就说明「页面签名的对象」与「节点解析出的对象」不一致，根因在链路里。
//
// last_tx 用真节点的 /tx_anchor，reward 用真节点的 /price/<size>（照报价），
// 免得假 anchor / 假价格的错误码污染结果。
//
// 用法：
//   ARWEAVE_JS_PATH=<包目录> node scripts/arjs-chunked-probe.cjs [data 字节数，默认 300000]

const path = require('path');

const pkg = process.env.ARWEAVE_JS_PATH;
if (!pkg) {
  console.error('需要 ARWEAVE_JS_PATH=<arweave-js 包目录>');
  process.exit(1);
}

const size = Number(process.argv[2] || 300000);
const NODE = process.argv[3] || 'https://arweave.net';

const Arweave = require(path.join(pkg, 'node', 'index.js'));
const txMod = require(path.join(pkg, 'node', 'lib', 'transaction.js'));
const Transaction = txMod.default || txMod.Transaction;

(async () => {
  const arweave = Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });

  const info = await fetch(NODE + '/info').then((r) => r.json());
  console.log('节点高度', info.height, '网络', info.network || '(未知)');

  const data = new Uint8Array(size);
  for (let i = 0; i < data.length; i++) data[i] = i & 0xff;

  const tx = new Transaction({ data });
  tx.data_size = data.byteLength.toString();
  tx.addTag('App-Name', 'read-only-git');
  tx.addTag('Content-Type', 'application/octet-stream');
  tx.addTag('Bundle-Format', 'binary');
  tx.addTag('Bundle-Version', '2.0.0');
  tx.addTag('Repo', 'probe');

  // 真 anchor 与真报价，排除这两项对错误码的干扰
  tx.last_tx = await fetch(NODE + '/tx_anchor').then((r) => r.text());
  tx.reward = await fetch(NODE + '/price/' + size).then((r) => r.text());
  console.log('anchor', tx.last_tx, 'reward', tx.reward);

  await tx.prepareChunks(data);
  const jwk = await arweave.wallets.generate(); // 测试钱包：余额 0，注定被拒
  await arweave.transactions.sign(tx, jwk);

  if (!(await arweave.transactions.verify(tx))) {
    throw new Error('签名自验没过，探针无效');
  }
  console.log('本地自验通过，块数', tx.chunks.chunks.length);

  // 照 transaction-uploader 的多块分支：拷贝、data 置空后序列化
  const zeroed = new Transaction(Object.assign({}, tx, { data: new Uint8Array(0) }));
  const body = JSON.stringify(zeroed);

  const post = await fetch(NODE + '/tx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
  });
  const postText = await post.text();
  console.log('POST /tx ->', post.status, postText);

  // 被拒的交易会把全部失败项留下来，以 410 + 错误码列表的形式可查
  await new Promise((r) => setTimeout(r, 1500));
  const st = await fetch(NODE + '/tx/' + tx.id + '/status');
  const stText = await st.text();
  console.log('GET /tx/<id>/status ->', st.status, stText);
  console.log('');
  console.log('结论：');
  if (/tx_signature_not_valid|tx_id_not_valid/.test(stText)) {
    console.log('  !! 签名链路有问题：页面签的对象与节点解析出的对象不一致');
  } else if (/overspend|tx_too_cheap/.test(stText)) {
    console.log('  签名链路无辜（挂的只是费用/余额项）：真链的 400 应从钱包余额与费用侧查');
  } else {
    console.log('  其他情况，按错误码逐项判断');
  }
})().catch((e) => {
  console.error('ERR', (e && e.stack) || e);
  process.exit(1);
});
