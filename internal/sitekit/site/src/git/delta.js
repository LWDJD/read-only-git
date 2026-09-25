import { readVarint } from './util.js'

// delta 声明的输出大小上限。git 对象极少超过这个量级（GitHub 上限 100MB），
// 而恶意 delta 会声明 2^30 甚至 2^32-1 来骗分配（测试报告 A2-2）。
const MAX_DELTA_RESULT = 512 * 1024 * 1024

/**
 * 应用 git delta 指令。
 *
 * 格式:
 *   baseSize   varint
 *   resultSize varint
 *   指令流:
 *     0x80 置位  -> copy：从 base 里搬运一段
 *     0x00       -> 非法
 *     其他        -> insert：紧跟其后的 N 个字节是字面量
 */
export function applyDelta(base, delta) {
  let r = readVarint(delta, 0)
  const baseSize = r.value
  if (baseSize !== base.length) {
    throw new Error(`delta base size mismatch: declared ${baseSize}, actual ${base.length}`)
  }

  r = readVarint(delta, r.next)
  const resultSize = r.value
  if (resultSize > MAX_DELTA_RESULT) {
    throw new Error(`delta 声明的输出大小 ${resultSize} 超过上限 ${MAX_DELTA_RESULT}，拒绝分配`)
  }
  let pos = r.next

  const out = new Uint8Array(resultSize)
  let outPos = 0

  while (pos < delta.length) {
    const cmd = delta[pos++]

    if (cmd & 0x80) {
      let copyOffset = 0
      let copySize = 0
      if (cmd & 0x01) copyOffset += delta[pos++]
      if (cmd & 0x02) copyOffset += delta[pos++] * 0x100
      if (cmd & 0x04) copyOffset += delta[pos++] * 0x10000
      if (cmd & 0x08) copyOffset += delta[pos++] * 0x1000000
      if (cmd & 0x10) copySize += delta[pos++]
      if (cmd & 0x20) copySize += delta[pos++] * 0x100
      if (cmd & 0x40) copySize += delta[pos++] * 0x10000
      if (copySize === 0) copySize = 0x10000

      if (copyOffset + copySize > base.length) {
        throw new Error('delta copy ran past the end of the base object')
      }
      if (outPos + copySize > resultSize) {
        throw new Error('delta 产出超出声明大小')
      }
      out.set(base.subarray(copyOffset, copyOffset + copySize), outPos)
      outPos += copySize
    } else if (cmd > 0) {
      if (pos + cmd > delta.length) {
        throw new Error('delta insert ran off the end')
      }
      if (outPos + cmd > resultSize) {
        throw new Error('delta 产出超出声明大小')
      }
      out.set(delta.subarray(pos, pos + cmd), outPos)
      outPos += cmd
      pos += cmd
    } else {
      throw new Error('invalid delta opcode 0x00')
    }
  }

  if (outPos !== resultSize) {
    throw new Error(`delta produced ${outPos} bytes, expected ${resultSize}`)
  }
  return out
}
