/**
 * 解析器自测：验证 objects / idx / pack / delta 的解析正确性。
 *
 * 用法:
 *   node scripts/selftest.mjs [裸仓库目录]
 *
 * 不给参数时会自己造一个 fixture（需要一个可用的 git），用完即删。
 * 它给 RemoteRepo 注入一个读本地文件的 fetch，其余逻辑与浏览器里完全一致。
 */
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { RemoteRepo } from '../public/src/git/repo.js'

function git(args, cwd) {
  return execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

/** 造一个带分支和标签的小仓库，返回裸仓库路径和它的内容清单 */
function makeFixture() {
  const root = mkdtempSync(join(tmpdir(), 'rog-selftest-'))
  const work = join(root, 'work')
  const bare = join(root, 'bare.git')

  mkdirSync(work, { recursive: true })
  git(['init', '--quiet', '-b', 'main'], work)
  git(['config', 'user.email', 'selftest@local'], work)
  git(['config', 'user.name', 'selftest'], work)

  writeFileSync(join(work, 'README.md'), '# fixture\n\n自测用仓库。\n', 'utf8')
  mkdirSync(join(work, 'src'), { recursive: true })
  writeFileSync(join(work, 'src', 'main.rs'), 'fn main() {\n    println!("hi");\n}\n', 'utf8')
  git(['add', '-A'], work)
  git(['commit', '--quiet', '-m', 'initial commit'], work)

  writeFileSync(join(work, 'src', 'main.rs'), 'fn main() {\n    println!("hello");\n}\n', 'utf8')
  git(['add', '-A'], work)
  git(['commit', '--quiet', '-m', 'tweak main'], work)

  git(['tag', 'v1.0.0'], work)
  git(['branch', 'dev'], work)

  git(['clone', '--bare', '--quiet', work, bare], root)

  return {
    bare,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  }
}

async function localFetch(input) {
  const href = typeof input === 'string' ? input : input.url
  const buf = await readFile(fileURLToPath(href))
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
  console.log(`  ${String(label).padEnd(22)} ${value}`)
}

/** 递归走完一棵树，返回 [路径, sha] 列表 */
async function walkTree(repo, treeSha, prefix = '') {
  const out = []
  for (const entry of await repo.getTree(treeSha)) {
    const full = prefix ? `${prefix}/${entry.name}` : entry.name
    if (entry.mode.replace(/^0+/, '') === '40000') {
      out.push(...await walkTree(repo, entry.sha, full))
    } else {
      out.push([full, entry.sha])
    }
  }
  return out
}

async function main() {
  let target = process.argv[2] ? resolve(process.argv[2]) : null
  let cleanup = null

  if (!target) {
    console.log('未指定裸仓库，自建 fixture…')
    try {
      const fixture = makeFixture()
      target = fixture.bare
      cleanup = fixture.cleanup
    } catch (err) {
      console.error(`建 fixture 失败：${err.message}`)
      console.error('')
      console.error('可以手动指定一个裸仓库：')
      console.error('  node scripts/selftest.mjs <裸仓库目录>')
      process.exit(1)
    }
  }

  try {
    const base = pathToFileURL(join(target, '.')).href + '/'
    console.log(`base ${base}`)

    const repo = new RemoteRepo(base, { fetch: localFetch })
    const t0 = Date.now()
    await repo.load()
    const loadMs = Date.now() - t0

    console.log('')
    console.log('— pack —')
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
    const commit = await repo.getCommit(head)

    console.log('')
    console.log('— HEAD 提交 —')
    line('sha', head.slice(0, 12))
    line('tree', commit.tree.slice(0, 12))
    line('parents', commit.parents.length)
    line('author', commit.author ? `${commit.author.name} <${commit.author.email}>` : '(无)')
    line('message', JSON.stringify(commit.message.trim()))

    console.log('')
    console.log('— 树遍历 —')
    const files = await walkTree(repo, commit.tree)
    for (const [path, sha] of files) line('blob', `${path}  ${sha.slice(0, 8)}`)

    console.log('')
    console.log('— 内容解压 —')
    let bytes = 0
    for (const [path] of files) {
      const { bytes: data } = await repo.readFile(head, path)
      bytes += data.length
      line(path, `${data.length} B`)
    }

    console.log('')
    console.log('— 提交历史 —')
    let n = 0
    for await (const { sha, commit: c } of repo.walkCommits(head, 10)) {
      console.log(`  ${sha.slice(0, 8)}  ${c.message.split('\n')[0]}`)
      n++
    }
    if (n === 0) console.log('  (空)')

    // 断言：只要有 commit 就必须能走通上面全部路径
    if (repo.packs.length === 0) throw new Error('没有加载到任何 pack')
    if (repo.objectCount === 0) throw new Error('pack 里没有对象')
    if (!head) throw new Error('没能解析 HEAD')
    if (files.length === 0) throw new Error('树是空的')
    if (bytes === 0) throw new Error('没有解出任何文件内容')
    if (n === 0) throw new Error('提交历史是空的')

    console.log('')
    console.log(`OK 全部解析路径通过（${files.length} 个文件，${n} 条提交）`)
  } finally {
    if (cleanup) cleanup()
  }
}

main().catch(err => {
  console.error('')
  console.error('FAILED:', err.message)
  console.error(err.stack)
  process.exit(1)
})
