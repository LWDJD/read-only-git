import { unzlib } from './zlib.js'
import { applyDelta } from './delta.js'
import { bytesToHex } from './util.js'

const TYPES = {
  1: 'commit',
  2: 'tree',
  3: 'blob',
  4: 'tag',
  6: 'ofs_delta',
  7: 'ref_delta',
}

/**
 * pack 文件读取器。
 *
 * 关键点：pack 里的 zlib 流没有长度字段，而浏览器的 DecompressionStream
 * 对尾部多余字节零容忍（会抛 "Trailing junk found after the end of the
 * compressed stream"）。解决办法是借助 pack index 的偏移表：把所有对象
 * 偏移排序后，某个对象的数据区间就是 [自身偏移, 下一个偏移)，切出来的
 * 字节刚好是该对象的完整压缩数据，不含任何残留。
 */
export class PackFile {
  constructor(packBytes, idx) {
    this.bytes = packBytes
    this.idx = idx

    const sig = String.fromCharCode(packBytes[0], packBytes[1], packBytes[2], packBytes[3])
    if (sig !== 'PACK') throw new Error('not a pack file')

    const dv = new DataView(packBytes.buffer, packBytes.byteOffset, packBytes.byteLength)
    this.version = dv.getUint32(4)
    this.count = dv.getUint32(8)
    this.dataEnd = packBytes.length - 20 // 末尾 20 字节是整包校验和
    this._cache = new Map()
  }

  size() {
    return this.count
  }

  async readBySha(hex, seen, depth) {
    const cached = this._cache.get(hex)
    if (cached) return cached
    const off = this.idx.offsetOf(hex)
    if (off < 0) throw new Error(`object not found in pack: ${hex}`)
    const obj = await this.readAt(off, seen, depth)
    this._cache.set(hex, obj)
    return obj
  }

  /**
   * seen 记下本轮已经走过的对象偏移，挡掉 delta 自指/互指的死循环；
   * depth 是 delta 链深度上限。两者都是恶意 pack 的防御（测试报告 A2-1）：
   * 一个几十字节的 pack 就能把标签页钉死在无限递归里。
   */
  async readAt(offset, seen, depth = 0) {
    if (depth > 64) throw new Error('delta 链条超过 64 层，拒绝解析')
    seen = seen || new Set()
    if (seen.has(offset)) throw new Error(`delta 基对象成环（偏移 ${offset}），拒绝解析`)
    seen.add(offset)

    const header = parseEntryHeader(this.bytes, offset)
    const zStart = offset + header.headerLen
    const zEnd = this.idx.nextOffsetAfter(offset, this.dataEnd)
    const compressed = this.bytes.subarray(zStart, zEnd)
    // 解压上限就用对象头声明的大小：超出即炸弹，立刻中止而不是先膨胀后校验
    const payload = await unzlib(compressed, header.size)

    if (header.type === 'commit' || header.type === 'tree' || header.type === 'blob' || header.type === 'tag') {
      if (payload.length !== header.size) {
        throw new Error(`object size mismatch at ${offset}: header ${header.size}, actual ${payload.length}`)
      }
      return { type: header.type, data: payload, offset }
    }

    if (header.type === 'ofs_delta') {
      const baseOffset = offset - header.baseOffset
      if (baseOffset < 0) throw new Error('ofs_delta points before the start of the pack')
      const base = await this.readAt(baseOffset, seen, depth + 1)
      const data = applyDelta(base.data, payload)
      return { type: base.type, data, offset, delta: true }
    }

    if (header.type === 'ref_delta') {
      const baseHex = bytesToHex(header.baseSha)
      const base = await this.readBySha(baseHex, seen, depth + 1)
      const data = applyDelta(base.data, payload)
      return { type: base.type, data, offset, delta: true }
    }

    throw new Error(`unsupported object type: ${header.type}`)
  }
}

export function parseEntryHeader(bytes, offset) {
  let pos = offset
  let c = bytes[pos++]
  const typeCode = (c >> 4) & 0x07
  let size = c & 0x0f
  let shift = 4

  while (c & 0x80) {
    c = bytes[pos++]
    size += (c & 0x7f) * Math.pow(2, shift)
    shift += 7
  }

  const type = TYPES[typeCode]
  if (!type) throw new Error(`unknown pack object type code: ${typeCode}`)

  let baseOffset = 0
  let baseSha = null

  if (type === 'ofs_delta') {
    let c2 = bytes[pos++]
    baseOffset = c2 & 0x7f
    while (c2 & 0x80) {
      c2 = bytes[pos++]
      baseOffset = (baseOffset + 1) * 128 + (c2 & 0x7f)
    }
  } else if (type === 'ref_delta') {
    baseSha = bytes.subarray(pos, pos + 20)
    pos += 20
  }

  return { type, size, headerLen: pos - offset, baseOffset, baseSha }
}
