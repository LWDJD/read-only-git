/**
 * zlib 解压。
 *
 * 重要约束：浏览器的 DecompressionStream 在压缩流结束后遇到任何多余字节
 * 都会抛出 "Trailing junk found after the end of the compressed stream"。
 * 因此调用方必须传入**精确的**压缩数据区间，不能有尾部残留。
 *
 * pack 文件里每个对象的边界靠 pack index 的 offset 表推算，见 pack.js。
 */

export async function unzlib(bytes) {
  if (bytes.length === 0) return new Uint8Array(0)

  if (typeof DecompressionStream === 'function') {
    return inflateWithStream(bytes)
  }
  // Node 回退（开发期自测用；构建产物不会走到这里）
  const { inflateSync } = await import('node:zlib')
  return new Uint8Array(inflateSync(bytes))
}

async function inflateWithStream(bytes) {
  const ds = new DecompressionStream('deflate')
  const writer = ds.writable.getWriter()
  const collecting = collectAll(ds.readable)
  const writing = writer.write(bytes).then(() => writer.close())

  // 两边都要 await：忽略 write 侧的错误会变成 unhandled rejection
  const [output] = await Promise.all([collecting, writing])
  return output
}

async function collectAll(readable) {
  const reader = readable.getReader()
  const chunks = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    chunks.push(value)
    total += value.length
  }
  return concat(chunks, total)
}

export function concat(chunks, total) {
  const size = total ?? chunks.reduce((n, c) => n + c.length, 0)
  const out = new Uint8Array(size)
  let off = 0
  for (const c of chunks) {
    out.set(c, off)
    off += c.length
  }
  return out
}
