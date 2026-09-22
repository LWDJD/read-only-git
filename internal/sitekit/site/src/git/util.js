const HEX = Array.from({ length: 256 }, (_, i) => i.toString(16).padStart(2, '0'))

export function bytesToHex(bytes, start = 0, end = bytes.length) {
  let out = ''
  for (let i = start; i < end; i++) out += HEX[bytes[i]]
  return out
}

export function hexToBytes(hex) {
  const out = new Uint8Array(hex.length >> 1)
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  }
  return out
}

export const utf8 = new TextDecoder('utf-8', { fatal: false })

export function text(bytes) {
  return utf8.decode(bytes)
}

/** LEB128 变长整数，git 在 delta 头与 pack 对象头里都用这种编码 */
export function readVarint(buf, offset) {
  let value = 0
  let shift = 0
  let pos = offset
  for (;;) {
    const c = buf[pos++]
    if (c === undefined) throw new Error('varint ran off the end')
    if (shift < 32) value += (c & 0x7f) * Math.pow(2, shift)
    shift += 7
    if ((c & 0x80) === 0) break
  }
  return { value, next: pos }
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

export function formatBytes(n) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}
