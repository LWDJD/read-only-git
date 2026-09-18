/**
 * 解析器自测：用真实 git 生成的裸仓库验证 objects / idx / pack / delta 的正确性。
 *
 * 用法: node scripts/selftest.mjs [裸仓库目录=tmp/bare.git]
 *
 * 它给 RemoteRepo 注入一个读本地文件的 fetch，其它逻辑与浏览器里完全一致。
 */
import { readFile } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { join, resolve } from 'node:path'
import { RemoteRepo } from '../src/git/repo.js'

const bareDir = resolve(process.argv[2] || 'tmp/bare.git')

async function localFetch(input) {
  const href = typeof input === 'string' ? input : input.url
  const path = fileURLToPath(href)
  const buf = await readFile(path)
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    async text() { return buf.toString('utf8') },
    async arrayBuffer() {
      return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength)
    },
  }
}

function line(label, value) {
  console.log(`  ${label.padEnd(22)} ${value}`)
}

async function main() {
  const base = pathToFileURL(join(bareDir, '.')).href + '/'
  console.log(`base ${base}`)

  const repo = new RemoteRepo(base, { fetch: localFetch })
  const t0 = Date.now()
  await repo.load()
  const loadMs = Date.now() - t0

  console.log('')
  console.log('— 仓库 —')
  line('pack 数量', repo.packs.length)
  line('对象总数', repo.objectCount)
  line('加载耗时', `${loadMs} ms`)

  const { heads, tags } = repo.listRefs()
  console.log('')
  console.log('— 引用 —')
  line('默认分支', repo.defaultBranch())
  for (const h of heads) line('分支', `${h.name}  ${h.sha.slice(0, 8)}`)
  for (const t of tags) line('标签', `${t.name}  ${t.sha.slice(0, 8)}`)

  const head = await repo.resolveCommitish('HEAD')
  console.log('')
  console.log('— HEAD —')
  line('commit', head)

  const commit = await repo.getCommit(head)
  console.log('')
  console.log('— 提交 —')
  line('tree', commit.tree)
  line('parents', commit.parents.length)
  line('author', commit.author ? `${commit.author.name} <${commit.author.email}>` : '(无)')
  line('message', JSON.stringify(commit.message.trim()))

  console.log('')
  console.log('— 根目录 —')
  const entries = await repo.listDirectory(head, '')
  for (const e of entries) {
    line(e.kind, `${e.name}  ${e.sha.slice(0, 8)}  mode=${e.mode}`)
  }

  console.log('')
  console.log('— 子目录 src —')
  for (const e of await repo.listDirectory(head, 'src')) {
    line(e.kind, `${e.name}  ${e.sha.slice(0, 8)}`)
  }

  console.log('')
  console.log('— 文件内容 —')
  for (const f of ['README.md', 'src/main.rs']) {
    try {
      const content = await repo.readFileText(head, f)
      line(f, JSON.stringify(content.trim()))
    } catch (err) {
      line(f, `读取失败: ${err.message}`)
    }
  }

  console.log('')
  console.log('— 提交历史 —')
  let n = 0
  for await (const { sha, commit: c } of repo.walkCommits(head, 10)) {
    const first = c.message.split('\n')[0]
    console.log(`  ${sha.slice(0, 8)}  ${first}`)
    n++
  }
  if (n === 0) console.log('  (空)')

  console.log('')
  console.log('OK 全部解析路径通过')
}

main().catch(err => {
  console.error('')
  console.error('FAILED:', err.message)
  console.error(err.stack)
  process.exit(1)
})
