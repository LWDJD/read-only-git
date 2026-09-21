// 生成分块对拍的向量文件，供 Go 侧测试比对。
//
// 为什么要有它：Go 侧切块的依据是 proof 的 offset，这套口径必须与
// arweave-js 完全一致，而「一致」不能只靠一次人工核对就算数。
// 这个脚本把 arweave-js 的输出固化成 JSON，Go 侧读它比对；
// 将来 arweave-js 改了口径，重跑一次就能立刻发现。
//
// 用法：
//   node scripts/arjs-vectors.cjs                    # 用已安装的 arweave
//   ARWEAVE_JS_PATH=<包目录> node scripts/arjs-vectors.cjs   # 用本地某份 arweave-js
//
// 输出到 internal/arweave/testdata/arjs-vectors.json

const fs = require('fs');
const path = require('path');

const pkg = process.env.ARWEAVE_JS_PATH || 'arweave';
const merkle = require(path.join(pkg, 'node', 'lib', 'merkle.js'));
const utils = require(path.join(pkg, 'node', 'lib', 'utils.js'));

// 这几个尺寸各自覆盖一个边界：
//   1          极小
//   32768      正好等于单块下限
//   262144     正好等于单块上限（会先切出一个零长尾块，建完树再丢掉）
//   262145     比上限多 1 字节（触发平分，而不是 262144 + 1）
//   300000     上限加一个零头
//   1048576    正好四块
const SIZES = [1, 32768, 262144, 262145, 300000, 1048576];

function fill(size) {
  const data = new Uint8Array(size);
  for (let i = 0; i < size; i++) data[i] = i & 0xff;
  return data;
}

(async () => {
  const cases = [];

  for (const size of SIZES) {
    const data = fill(size);
    const r = await merkle.generateTransactionChunks(data);
    cases.push({
      size,
      offsets: r.proofs.map((p) => p.offset),
      dataRoot: utils.bufferTob64Url(r.data_root),
      chunkHashes: r.chunks.map((c) => Buffer.from(c.dataHash).toString('hex')),
      proofLengths: r.proofs.map((p) => p.proof.length),
    });
  }

  const out = {
    generatedBy: 'arweave-js ' + require(path.join(pkg, 'package.json')).version,
    fill: 'data[i] = i & 0xff',
    note: 'offset 是每块末字节在整份数据里的索引。',
    cases,
  };

  const target = path.join(__dirname, '..', 'internal', 'arweave', 'testdata', 'arjs-vectors.json');
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, JSON.stringify(out, null, 1) + '\n');
  console.log('written ' + target);
})();
