/**
 * 环境探测：确认浏览器原生解压 API 的行为，以及能否读取真实 git 对象。
 * 仅用于开发期验证，不参与构建产物。
 *
 * 用法: node scripts/probe.mjs [裸仓库或普通仓库路径]
 */
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { deflateSync } from 'node:zlib'

function concat(chunks) {
  const total = chunks.reduce((n, c) => n + c.length, 0)
  const out = new Uint8Array(total)
  let off = 0
  for (const c of chunks) { out.set(c, off); off += c.length }
  return out
}

async function inflateStream(bytes) {
  const ds = new DecompressionStream('deflate')
  const writer = ds.writable.getWriter()
  const readAll = (async () => {
    const chunks = []
    const reader = ds.readable.getReader()
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      chunks.push(value)
    }
    return concat(chunks)
  })()
  writer.write(bytes)
  writer.close()
  return readAll
}

async function probeJunk() {
  const payload = new TextEncoder().encode('hello world')
  const compressed = new Uint8Array(deflateSync(payload))
  const withJunk = new Uint8Array(compressed.length + 8)
  withJunk.set(compressed)
  withJunk.fill(0xaa, compressed.length)

  try {
    const out = await inflateStream(withJunk)
    console.log(`[junk]  容忍尾部垃圾 -> "${new TextDecoder().decode(out)}"`)
  } catch (e) {
    console.log(`[junk]  拒绝尾部垃圾 -> ${e.constructor.name}: ${e.message}`)
  }
}

async function probeObjects(repoPath) {
  const root = resolve(repoPath || '.test/repo')
  const candidates = [join(root, '.git'), root]
  const gitDir = candidates.find(d => existsSync(join(d, 'objects')))
  if (!gitDir) {
    console.log(`[loose] ${root} 里找不到 objects 目录，跳过`)
    return
  }

  const dir = join(gitDir, 'objects')
  const files = []
  for (const top of readdirSync(dir)) {
    if (top.length !== 2) continue
    const sub = join(dir, top)
    if (!statSync(sub).isDirectory()) continue
    for (const rest of readdirSync(sub)) files.push(join(sub, rest))
  }

  console.log(`[loose] ${gitDir} 下找到 ${files.length} 个松散对象`)
  for (const f of files) {
    const raw = new Uint8Array(readFileSync(f))
    const out = await inflateStream(raw)
    let i = 0
    while (i < out.length && out[i] !== 0) i++
    const header = new TextDecoder().decode(out.subarray(0, i))
    console.log(`  ${header.padEnd(20)} ${raw.length}B -> ${out.length}B`)
  }

  const packDir = join(dir, 'pack')
  if (existsSync(packDir)) {
    const packs = readdirSync(packDir).filter(f => f.endsWith('.pack'))
    console.log(`[pack] 找到 ${packs.length} 个 pack: ${packs.join(', ') || '(无)'}`)
  }
}

async function main() {
  await probeJunk()
  await probeObjects(process.argv[2])
}

main().catch(e => { console.error('FAILED', e); process.exit(1) })
