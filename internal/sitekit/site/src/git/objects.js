import { bytesToHex, text } from './util.js'

/**
 * tree 对象的二进制格式：
 *   重复 { <mode> SP <name> NUL <20 字节 sha> }
 * 注意 git 不会写入条目数量，靠长度判断结束。
 */
export function parseTree(data) {
  const entries = []
  let pos = 0

  while (pos < data.length) {
    let sp = pos
    while (sp < data.length && data[sp] !== 0x20) sp++
    if (sp >= data.length) break

    let nul = sp + 1
    while (nul < data.length && data[nul] !== 0) nul++
    if (nul + 21 > data.length) break

    entries.push({
      mode: text(data.subarray(pos, sp)),
      name: text(data.subarray(sp + 1, nul)),
      sha: bytesToHex(data, nul + 1, nul + 21),
    })

    pos = nul + 21
  }

  return entries
}

export const MODE = {
  tree: '40000',
  blob: '100644',
  executable: '100755',
  symlink: '120000',
  submodule: '160000',
}

export function modeKind(mode) {
  const m = mode.replace(/^0+/, '') || '0'
  if (m === '40000' || m === '40') return 'tree'
  if (m === '160000') return 'submodule'
  if (m === '120000') return 'symlink'
  if (m === '100755') return 'exec'
  return 'file'
}

export function parseCommit(data) {
  const raw = text(data)
  const split = raw.indexOf('\n\n')
  const head = split < 0 ? raw : raw.slice(0, split)
  const message = split < 0 ? '' : raw.slice(split + 2)

  const commit = { tree: '', parents: [], author: null, committer: null, message, headers: [] }

  for (const line of head.split('\n')) {
    if (line.startsWith('tree ')) commit.tree = line.slice(5)
    else if (line.startsWith('parent ')) commit.parents.push(line.slice(7))
    else if (line.startsWith('author ')) commit.author = parseSignature(line.slice(7))
    else if (line.startsWith('committer ')) commit.committer = parseSignature(line.slice(10))
    else if (line) commit.headers.push(line)
  }

  return commit
}

export function parseTag(data) {
  const raw = text(data)
  const split = raw.indexOf('\n\n')
  const head = split < 0 ? raw : raw.slice(0, split)
  const message = split < 0 ? '' : raw.slice(split + 2)

  const tag = { object: '', type: '', tag: '', tagger: null, message }

  for (const line of head.split('\n')) {
    if (line.startsWith('object ')) tag.object = line.slice(7)
    else if (line.startsWith('type ')) tag.type = line.slice(5)
    else if (line.startsWith('tag ')) tag.tag = line.slice(4)
    else if (line.startsWith('tagger ')) tag.tagger = parseSignature(line.slice(7))
  }

  return tag
}

/** "Name <mail> 1700000000 +0800" */
export function parseSignature(sig) {
  const m = /^(.*?)\s*<([^>]*)>\s*(\d+)\s*([+-]\d{4})$/.exec(sig.trim())
  if (!m) return { name: sig.trim(), email: '', date: null, tz: '' }
  return {
    name: m[1],
    email: m[2],
    date: new Date(Number(m[3]) * 1000),
    tz: m[4],
  }
}
