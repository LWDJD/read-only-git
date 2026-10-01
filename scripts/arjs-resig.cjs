// 对「已签名未提交」的 pending 文件，用 arweave-js 重算一次签名输入，
// 与 Go 复刻的节点口径对拍。差异逐项打印。
// 用法：ARWEAVE_JS_PATH=<包目录> node scripts/arjs-resig.cjs <pending.json>
const fs = require('fs');
const path = require('path');

const pkg = process.env.ARWEAVE_JS_PATH;
const file = process.argv[2];
if (!pkg || !file) {
  console.error('用法: ARWEAVE_JS_PATH=<包目录> node scripts/arjs-resig.cjs <pending.json>');
  process.exit(1);
}

const txMod = require(path.join(pkg, 'node', 'lib', 'transaction.js'));
const Transaction = txMod.default || txMod.Transaction;
const utils = require(path.join(pkg, 'node', 'lib', 'utils.js'));

const p = JSON.parse(fs.readFileSync(file, 'utf8'));
const sig = p.sig;

// 页面流程是 addTag（编码后存），这里照做
const tx = new Transaction({ data: new Uint8Array(0) });
for (const t of p.tags) tx.addTag(t.name, t.value);
tx.format = 2;
tx.id = sig.id;
tx.owner = sig.owner;
tx.signature = sig.signature;
tx.reward = sig.reward;
tx.last_tx = sig.last_tx;
tx.data_root = sig.data_root;
tx.data_size = String(sig.data_size);

(async () => {
  const seg = await tx.getSignatureData();
  console.log('arweave-js getSignatureData:', Buffer.from(seg).toString('hex'));

  // 用 arweave-js 自己的 verify 裁决：它内部就是 crypto.verify(owner, getSignatureData(), sig)
  const Arweave = require(path.join(pkg, 'node', 'index.js'));
  const arweave = Arweave.init({ host: 'arweave.net', port: 443, protocol: 'https' });
  try {
    const ok = await arweave.transactions.verify(tx);
    console.log('arweave-js transactions.verify ->', ok);
  } catch (e) {
    console.log('arweave-js transactions.verify -> threw:', e.message);
  }

  console.log('tags 内部形态（addTag 后）:');
  for (const t of tx.tags) console.log('  ', JSON.stringify(t.name), JSON.stringify(t.value));

  // 各字段在签名输入里的实际字节
  const fields = {
    format: utils.stringToBuffer(tx.format.toString()),
    owner: tx.get('owner', { decode: true, string: false }),
    target: tx.get('target', { decode: true, string: false }),
    quantity: utils.stringToBuffer(tx.quantity),
    reward: utils.stringToBuffer(tx.reward),
    last_tx: tx.get('last_tx', { decode: true, string: false }),
    data_size: utils.stringToBuffer(tx.data_size),
    data_root: tx.get('data_root', { decode: true, string: false }),
  };
  for (const [k, v] of Object.entries(fields)) {
    console.log(`  ${k}: ${Buffer.from(v).toString('hex')}`);
  }
})();
