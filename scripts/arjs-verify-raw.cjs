// 用 WebCrypto 直接验 pending 里的签名：与钱包/浏览器同族的实现。
// 与 Go 的暴力验法对照，判定「签名到底是不是对 seg 的」。
// 用法：node scripts/arjs-verify-raw.cjs <pending.json>
const fs = require('fs');
const { webcrypto } = require('crypto');

const p = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const sig = p.sig;
const b64u = (s) => Uint8Array.from(Buffer.from(s, 'base64url'));

(async () => {
  const segHex =
    '0d04b163f26fc9fe78f82e1caca580097f0c1c03b612c9dd8375724b3fe8b76d6328d577d225968f57fba6d8a85f365c';
  const seg = Uint8Array.from(Buffer.from(segHex, 'hex'));
  const signature = b64u(sig.signature);

  const importKey = (alg) =>
    webcrypto.subtle.importKey(
      'jwk',
      { kty: 'RSA', e: 'AQAB', n: sig.owner, ext: true },
      alg,
      false,
      ['verify']
    );

  // RSA-PSS，各种 salt
  for (const saltLength of [0, 20, 32, 40, 48, 62, 64, 128, 222, 256, 478]) {
    try {
      const key = await importKey({ name: 'RSA-PSS', hash: 'SHA-256' });
      const ok = await webcrypto.subtle.verify(
        { name: 'RSA-PSS', saltLength },
        key,
        signature,
        seg
      );
      console.log(`RSA-PSS salt=${saltLength} ->`, ok ? 'PASS <- 就是这种' : 'fail');
    } catch (e) {
      console.log(`RSA-PSS salt=${saltLength} -> error ${e.message}`);
    }
  }

  // RSASSA-PKCS1-v1_5（WebCrypto 内部做 SHA-256）
  try {
    const key = await importKey({ name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' });
    const ok = await webcrypto.subtle.verify(
      { name: 'RSASSA-PKCS1-v1_5' },
      key,
      signature,
      seg
    );
    console.log('RSASSA-PKCS1-v1_5 SHA-256 ->', ok ? 'PASS <- 就是这种' : 'fail');
  } catch (e) {
    console.log('RSASSA-PKCS1-v1_5 -> error', e.message);
  }

  // 消息先 hash 再当数据验（双 hash 猜测）
  try {
    const digest = Uint8Array.from(await webcrypto.subtle.digest('SHA-256', seg));
    const key = await importKey({ name: 'RSA-PSS', hash: 'SHA-256' });
    const ok = await webcrypto.subtle.verify(
      { name: 'RSA-PSS', saltLength: 32 },
      key,
      signature,
      digest
    );
    console.log('RSA-PSS salt=32 对 SHA256(seg) 双重摘要 ->', ok ? 'PASS <- 就是这种' : 'fail');
  } catch (e) {
    console.log('双重摘要 -> error', e.message);
  }
})();
