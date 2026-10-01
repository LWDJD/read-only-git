import { PackIndex } from './idx.js'
import { PackFile } from './pack.js'
import { parseCommit, parseTag, parseTree, modeKind } from './objects.js'
import { text } from './util.js'

/**
 * 一个远端只读仓库。
 *
 * baseUrl 指向 xxx.git/ 目录，所有请求都是普通静态文件 GET：
 *   HEAD
 *   info/refs
 *   objects/info/packs
 *   objects/pack/*.pack
 *   objects/pack/*.idx
 */
export class RemoteRepo {
  constructor(baseUrl, options = {}) {
    this.base = baseUrl.endsWith('/') ? baseUrl : `${baseUrl}/`
    this.fetchImpl = options.fetch || globalThis.fetch.bind(globalThis)
    this.refs = new Map()
    this.peeled = new Map()
    this.headRaw = ''
    this.packs = []
    this._objects = new Map()
    this._trees = new Map()
    this._commits = new Map()
  }

  async request(path, as = 'text') {
    const url = new URL(path, this.base).href
    const res = await this.fetchImpl(url)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText} @ ${url}`)
    return as === 'text' ? res.text() : new Uint8Array(await res.arrayBuffer())
  }

  async load() {
    const [infoRefs, head, packsText] = await Promise.all([
      this.request('info/refs'),
      this.request('HEAD'),
      this.request('objects/info/packs'),
    ])

    parseInfoRefs(infoRefs, this.refs, this.peeled)
    this.headRaw = head.trim()

    const packNames = parsePackList(packsText)
    for (const name of packNames) {
      const idxName = name.replace(/\.pack$/, '.idx')
      const [idxBytes, packBytes] = await Promise.all([
        this.request(`objects/pack/${idxName}`, 'bytes'),
        this.request(`objects/pack/${name}`, 'bytes'),
      ])
      this.packs.push(new PackFile(packBytes, new PackIndex(idxBytes)))
    }

    return this
  }

  get objectCount() {
    return this.packs.reduce((n, p) => n + p.size(), 0)
  }

  listRefs() {
    const heads = []
    const tags = []
    for (const [name, sha] of this.refs) {
      if (name.startsWith('refs/heads/')) heads.push({ name: name.slice(11), sha })
      else if (name.startsWith('refs/tags/')) tags.push({ name: name.slice(10), sha })
    }
    heads.sort((a, b) => a.name.localeCompare(b.name))
    // 标签按版本号自然序，否则 v0.9.0 会排在 v1.0.0 前面
    tags.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
    return { heads, tags }
  }

  defaultBranch() {
    if (this.headRaw.startsWith('ref: ')) {
      const ref = this.headRaw.slice(5).trim()
      if (ref.startsWith('refs/heads/')) return ref.slice(11)
    }
    const { heads } = this.listRefs()
    return heads.length ? heads[0].name : 'main'
  }

  async readRaw(sha) {
    const cached = this._objects.get(sha)
    if (cached) return cached

    for (const pack of this.packs) {
      if (pack.idx.find(sha) >= 0) {
        const obj = await pack.readBySha(sha)
        this._objects.set(sha, obj)
        return obj
      }
    }
    throw new Error(`object not found: ${sha}`)
  }

  /** 解析出 commit sha；遇到附注标签会逐层 peel */
  async resolveCommitish(ref) {
    let sha = this.resolveRef(ref)

    for (let guard = 0; guard < 64; guard++) {
      const obj = await this.readRaw(sha)
      if (obj.type === 'tag') {
        sha = parseTag(obj.data).object
        continue
      }
      if (obj.type === 'commit') return sha
      throw new Error(`ref does not point at a commit: ${ref}`)
    }
    throw new Error('tag chain too deep')
  }

  resolveRef(ref) {
    if (!ref || ref === 'HEAD') {
      if (this.headRaw.startsWith('ref: ')) return this.resolveRef(this.headRaw.slice(5).trim())
      return this.headRaw
    }
    const candidates = [
      ref,
      `refs/heads/${ref}`,
      `refs/tags/${ref}`,
    ]
    for (const c of candidates) {
      const sha = this.refs.get(c)
      if (sha) return sha
    }
    if (/^[0-9a-f]{40}$/.test(ref)) return ref
    throw new Error(`unknown ref: ${ref}`)
  }

  async getCommit(sha) {
    const cached = this._commits.get(sha)
    if (cached) return cached
    const obj = await this.readRaw(sha)
    if (obj.type !== 'commit') throw new Error(`not a commit: ${sha}`)
    const commit = parseCommit(obj.data)
    this._commits.set(sha, commit)
    return commit
  }

  async getTree(sha) {
    const cached = this._trees.get(sha)
    if (cached) return cached
    const obj = await this.readRaw(sha)
    if (obj.type !== 'tree') throw new Error(`not a tree: ${sha}`)
    const entries = parseTree(obj.data)
    this._trees.set(sha, entries)
    return entries
  }

  /** 返回目录列表；路径不存在时抛错 */
  async listDirectory(commitSha, path = '') {
    const parts = splitPath(path)
    let treeSha = (await this.getCommit(commitSha)).tree

    for (const part of parts) {
      const entries = await this.getTree(treeSha)
      const hit = entries.find(e => e.name === part)
      if (!hit) throw new Error(`no such path: ${path}`)
      if (modeKind(hit.mode) !== 'tree') throw new Error(`not a directory: ${path}`)
      treeSha = hit.sha
    }

    const entries = await this.getTree(treeSha)
    return entries.map(e => ({
      name: e.name,
      sha: e.sha,
      mode: e.mode,
      kind: modeKind(e.mode),
    })).sort(compareEntries)
  }

  /** 读取文件内容，返回 Uint8Array */
  async readFile(commitSha, path) {
    const parts = splitPath(path)
    const fileName = parts.pop()
    const dirEntries = await this.listDirectory(commitSha, parts.join('/'))
    const hit = dirEntries.find(e => e.name === fileName)
    if (!hit) throw new Error(`no such file: ${path}`)
    if (hit.kind === 'tree') throw new Error(`not a file: ${path}`)

    const obj = await this.readRaw(hit.sha)
    return { entry: hit, object: obj, bytes: obj.data }
  }

  async readFileText(commitSha, path) {
    const { bytes } = await this.readFile(commitSha, path)
    return text(bytes)
  }

  async findReadme(commitSha) {
    const entries = await this.listDirectory(commitSha, '')
    const hit = entries.find(e => e.kind === 'file' && /^readme(\.|$)/i.test(e.name))
    return hit ? hit.name : null
  }

  /** 沿第一父提交回溯 */
  async *walkCommits(startSha, limit = 50) {
    let cur = startSha
    for (let i = 0; cur && i < limit; i++) {
      const commit = await this.getCommit(cur)
      yield { sha: cur, commit }
      cur = commit.parents[0]
    }
  }
}

function splitPath(path) {
  return String(path || '').split('/').filter(Boolean)
}

function parseInfoRefs(source, refs, peeled) {
  for (const line of source.split('\n')) {
    const t = line.trim()
    if (!t) continue
    const tab = t.indexOf('\t')
    if (tab < 0) continue
    const sha = t.slice(0, tab)
    const name = t.slice(tab + 1)
    if (name.endsWith('^{}')) peeled.set(name.slice(0, -3), sha)
    else refs.set(name, sha)
  }
}

function parsePackList(source) {
  const out = []
  for (const line of source.split('\n')) {
    const t = line.trim()
    if (t.startsWith('P ')) out.push(t.slice(2))
  }
  return out
}

function compareEntries(a, b) {
  const rank = k => (k === 'tree' ? 0 : 1)
  const ra = rank(a.kind)
  const rb = rank(b.kind)
  if (ra !== rb) return ra - rb
  return a.name.localeCompare(b.name)
}
