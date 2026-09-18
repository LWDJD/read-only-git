#!/usr/bin/env node
/**
 * 零依赖静态服务器，用于本地预览与端到端测试。
 *
 * 用法:
 *   node scripts/serve.mjs [根目录] [端口=4173]
 *
 *   根目录省略时取脚本旁边的 ../public，跟从哪个目录调用无关。
 *
 * 刻意不依赖任何 npm 包：这个项目的目标是在任何静态托管上跑，
 * 本地预览也不应该需要构建工具。
 *
 * 行为要点（与真实静态托管对齐）:
 *   - 支持 Range 请求（git dumb 协议会用它做局部下载）
 *   - 不解析 .git 路径（真实托管也大多不解析，但我们不能依赖托管行为）
 *   - 目录请求回落到 index.html
 */
import { createServer } from 'node:http'
import { createReadStream, promises as fs } from 'node:fs'
import { dirname, extname, join, normalize, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(process.argv[2] || join(here, '..', 'public'))
const port = Number(process.argv[3] || 4173)

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.ico': 'image/x-icon',
  '.txt': 'text/plain; charset=utf-8',
  '.md': 'text/markdown; charset=utf-8',
  '.pack': 'application/octet-stream',
  '.idx': 'application/octet-stream',
}

function typeFor(file) {
  return MIME[extname(file).toLowerCase()] || 'application/octet-stream'
}

async function resolveTarget(urlPath) {
  const clean = decodeURIComponent(urlPath.split('?')[0])
  const safe = normalize(clean).replace(/^([/\\])+/, '')
  let abs = join(root, safe)
  if (!abs.startsWith(root + sep) && abs !== root) return null

  try {
    const st = await fs.stat(abs)
    if (st.isDirectory()) {
      const index = join(abs, 'index.html')
      try {
        const ist = await fs.stat(index)
        if (ist.isFile()) return { abs: index, size: ist.size }
      } catch { /* 没有 index.html，交给调用方处理 */ }
      return { abs, size: st.size, isDir: true }
    }
    return { abs, size: st.size }
  } catch {
    return null
  }
}

const server = createServer(async (req, res) => {
  const target = await resolveTarget(req.url || '/')

  if (!target) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end('404 Not Found\n')
    log(404, req.url)
    return
  }

  if (target.isDir) {
    // 目录且无 index.html：列出内容，便于人工核对 git 目录结构
    const entries = await fs.readdir(target.abs)
    const body = entries.length
      ? entries.map(e => `${e}`).join('\n') + '\n'
      : '(空目录)\n'
    res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end(body)
    log(200, req.url)
    return
  }

  const range = req.headers.range
  const headers = {
    'Content-Type': typeFor(target.abs),
    'Accept-Ranges': 'bytes',
    'Cache-Control': 'no-store',
    'Access-Control-Allow-Origin': '*',
  }

  if (range) {
    const m = /^bytes=(\d*)-(\d*)$/.exec(range.trim())
    if (m) {
      let start = m[1] === '' ? undefined : Number(m[1])
      let end = m[2] === '' ? undefined : Number(m[2])
      if (start === undefined) {
        start = Math.max(0, target.size - (end ?? 0))
        end = target.size - 1
      } else if (end === undefined || end >= target.size) {
        end = target.size - 1
      }
      if (start > end || start >= target.size) {
        res.writeHead(416, { 'Content-Range': `bytes */${target.size}` })
        res.end()
        log(416, req.url)
        return
      }
      headers['Content-Range'] = `bytes ${start}-${end}/${target.size}`
      headers['Content-Length'] = String(end - start + 1)
      res.writeHead(206, headers)
      createReadStream(target.abs, { start, end }).pipe(res)
      log(206, req.url)
      return
    }
  }

  headers['Content-Length'] = String(target.size)
  res.writeHead(200, headers)
  createReadStream(target.abs).pipe(res)
  log(200, req.url)
})

function log(status, url) {
  console.log(`${String(status).padEnd(4)} ${url}`)
}

server.on('error', (err) => {
  if (err.code === 'EADDRINUSE') {
    console.error(`端口 ${port} 已被占用。`)
    console.error('换一个端口重试：')
    console.error(`  node scripts/serve.mjs "${root}" ${Number(port) + 1}`)
    console.error('（Windows 上可以先用 netstat -ano | findstr :' + port + ' 找出占用进程）')
    process.exit(1)
  }
  throw err
})

server.listen(port, () => {
  console.log(`read-only-git preview`)
  console.log(`  root  ${root}`)
  console.log(`  url   http://localhost:${port}/`)
  console.log('  stop  Ctrl+C')
  console.log('')
})
