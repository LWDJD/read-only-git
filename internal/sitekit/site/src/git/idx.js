/**
 * pack index (.idx) 解析。
 *
 * 支持 v2 格式（现代 git 默认）：
 *   magic(4) version(4)
 *   fanout[256] * u32
 *   sha1[count] * 20
 *   crc32[count] * u32
 *   offset[count] * u32      (最高位为 1 时指向 8 字节大偏移表)
 *   largeOffset[] * u64
 *   packChecksum(20) idxChecksum(20)
 *
 * 我们只关心两件事：
 *   1. 给定 sha 找到 pack 内偏移（用于按需读取）
 *   2. 拿到全部偏移并排序（用于推算每个对象的字节边界）
 */

const MAGIC = [0xff, 0x74, 0x4f, 0x63] // "\377tOc"
const SHA_BYTES = 20

export class PackIndex {
  constructor(bytes) {
    this.bytes = bytes
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)

    if (this.view.byteLength < 8) throw new Error('idx too short')
    const isV2 = MAGIC.every((b, i) => bytes[i] === b)
    if (!isV2) throw new Error('only idx v2 is supported')

    const version = this.view.getUint32(4)
    if (version !== 2) throw new Error(`unsupported idx version: ${version}`)

    let pos = 8
    this.fanout = new Array(256)
    for (let i = 0; i < 256; i++) {
      this.fanout[i] = this.view.getUint32(pos)
      pos += 4
    }
    this.count = this.fanout[255]

    this.shaStart = pos
    pos += this.count * SHA_BYTES
    pos += this.count * 4 // crc32，跳过
    this.offsetStart = pos
    pos += this.count * 4
    this.largeOffsetStart = pos

    this._shaCache = new Map()
    this._sortedOffsets = null
  }

  shaAt(i) {
    if (this._shaCache.has(i)) return this._shaCache.get(i)
    const base = this.shaStart + i * SHA_BYTES
    let hex = ''
    for (let k = 0; k < SHA_BYTES; k++) {
      hex += this.bytes[base + k].toString(16).padStart(2, '0')
    }
    this._shaCache.set(i, hex)
    return hex
  }

  offsetAt(i) {
    const raw = this.view.getUint32(this.offsetStart + i * 4)
    if ((raw & 0x80000000) === 0) return raw
    const idx = raw & 0x7fffffff
    const hi = this.view.getUint32(this.largeOffsetStart + idx * 8)
    const lo = this.view.getUint32(this.largeOffsetStart + idx * 8 + 4)
    return hi * 0x100000000 + lo
  }

  /** 二分查找 sha，返回索引或 -1 */
  find(hexSha) {
    const target = hexToBytes(hexSha)
    let lo = 0
    let hi = this.count - 1
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      const cmp = compareBytes(this.bytes, this.shaStart + mid * SHA_BYTES, target)
      if (cmp === 0) return mid
      if (cmp < 0) lo = mid + 1
      else hi = mid - 1
    }
    return -1
  }

  offsetOf(hexSha) {
    const i = this.find(hexSha)
    return i < 0 ? -1 : this.offsetAt(i)
  }

  /** 全部偏移，升序。用于推算对象在 pack 中的字节边界 */
  sortedOffsets() {
    if (!this._sortedOffsets) {
      const list = new Array(this.count)
      for (let i = 0; i < this.count; i++) list[i] = this.offsetAt(i)
      list.sort((a, b) => a - b)
      this._sortedOffsets = list
    }
    return this._sortedOffsets
  }

  /** 大于 off 的最小偏移；没有则返回 fallback */
  nextOffsetAfter(off, fallback) {
    const list = this.sortedOffsets()
    let lo = 0
    let hi = list.length - 1
    let ans = -1
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (list[mid] > off) {
        ans = list[mid]
        hi = mid - 1
      } else {
        lo = mid + 1
      }
    }
    return ans === -1 ? fallback : ans
  }

  allShas() {
    const out = new Array(this.count)
    for (let i = 0; i < this.count; i++) out[i] = this.shaAt(i)
    return out
  }
}

function hexToBytes(hex) {
  const out = new Uint8Array(hex.length / 2)
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  }
  return out
}

function compareBytes(buf, offset, target) {
  for (let i = 0; i < target.length; i++) {
    const a = buf[offset + i]
    const b = target[i]
    if (a !== b) return a < b ? -1 : 1
  }
  return 0
}
