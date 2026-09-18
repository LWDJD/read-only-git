import {
  h, mount, fmtDate, shortSha, fmtBytes,
  bytesLookBinary, isImage, looksBinary,
} from './ui.js'
import {
  treeHref, blobHref, commitsHref, repoHref, cloneUrl,
  branchesHref, tagsHref,
} from './router.js'
import { loadRegistry, openRepo, stripGitSuffix } from './store.js'
import { renderMarkdown, stripMarkdown } from './markdown.js'

export function renderLoading(app, message) {
  mount(app, h('p', { class: 'muted', text: message || '加载中…' }))
}

export function renderError(app, err) {
  mount(app, h('div', { class: 'error', text: String((err && err.message) || err) }))
}

function link(href, text, className) {
  return h('a', { href, class: className, text })
}

function firstLine(commit) {
  const line = ((commit && commit.message) || '').split('\n')[0]
  return stripMarkdown(line) || '(无提交说明)'
}

/** tree 里除了目录，还有 symlink / submodule，这里只认普通文件 */
function isFileEntry(entry) {
  return entry.kind === 'file' || entry.kind === 'exec'
}

/* --- 仓库页公共骨架 ------------------------------------------------------ */

/** refs / 默认分支 / HEAD 提交，仓库页各视图都要用 */
async function loadChrome(name) {
  const repo = await openRepo(name)
  const { heads, tags } = repo.listRefs()
  const branch = repo.defaultBranch()
  const headSha = await repo.resolveCommitish('HEAD')
  const commit = await repo.getCommit(headSha)
  return { repo, heads, tags, branch, headSha, commit }
}

/** repository.json 里给这个仓库写的简介，没有就返回空串 */
async function registryDescription(name) {
  try {
    const { repositories } = await loadRegistry()
    const hit = repositories.find(r => r.name === stripGitSuffix(name))
    return (hit && hit.description) || ''
  } catch {
    return ''
  }
}

function repoHeader({ name, description }) {
  return h('header', { class: 'repo-header' },
    h('div', { class: 'repo-title' },
      h('span', { class: 'repo-mark', text: '⬢' }),
      h('span', { class: 'repo-name', text: `${stripGitSuffix(name)}.git` }),
      h('span', { class: 'pill pill-muted', text: '只读' }),
    ),
    description ? h('p', { class: 'repo-desc', text: description }) : null,
  )
}

function repoNav({ name, ref, active, heads, tags }) {
  const item = (label, href, key, count) => {
    const node = h('a', { class: `nav-item${active === key ? ' is-active' : ''}`, href },
      h('span', { text: label }))
    if (typeof count === 'number' && count > 0) {
      node.append(h('span', { class: 'nav-count', text: String(count) }))
    }
    return node
  }

  return h('nav', { class: 'repo-nav' },
    item('Code', treeHref(name, ref, ''), 'code'),
    item('提交', commitsHref(name, ref), 'commits'),
    item('分支', branchesHref(name), 'branches', heads.length),
    item('标签', tagsHref(name), 'tags', tags.length),
  )
}

function sidePanel(title, ...body) {
  return h('section', { class: 'side-panel' },
    h('h2', { class: 'side-title', text: title }),
    h('div', { class: 'side-body' }, body),
  )
}

function aboutPanel({ name, ref, description, license, heads, tags, repo }) {
  const clone = cloneUrl(name)

  const copyBtn = h('button', {
    type: 'button',
    class: 'copy-btn',
    text: '复制',
    onclick: async (e) => {
      const btn = e.currentTarget
      try {
        await navigator.clipboard.writeText(`git clone ${clone}`)
        btn.textContent = '已复制'
      } catch {
        btn.textContent = '失败'
      }
      setTimeout(() => { btn.textContent = '复制' }, 1500)
    },
  })

  return sidePanel('About',
    description
      ? h('p', { class: 'about-desc', text: description })
      : h('p', { class: 'about-desc muted', text: '仓库没有写简介' }),

    h('ul', { class: 'about-list' },
      h('li', {},
        h('span', { class: 'about-ico', text: '🔗' }),
        h('code', { class: 'about-clone', text: clone.replace(/^https?:\/\//, '') }),
        copyBtn,
      ),
      license
        ? h('li', {},
          h('span', { class: 'about-ico', text: '📄' }),
          h('a', { href: blobHref(name, ref, license.path), text: license.label }),
        )
        : null,
      h('li', {},
        h('span', { class: 'about-ico', text: '⭐' }),
        h('span', { text: `${repo.objectCount} 个对象 · ${heads.length} 个分支 · ${tags.length} 个标签` }),
      ),
    ),
  )
}

function releasesPanel({ name, tags }) {
  if (!tags.length) {
    return sidePanel('Releases', h('p', { class: 'muted', text: '暂无发布' }))
  }

  // tags 是升序版本序，这里倒过来，最新的排最上面
  const shown = [...tags].reverse()
  const limited = shown.slice(0, 6)

  return sidePanel('Releases',
    h('ul', { class: 'release-list' },
      limited.map((tag, i) => h('li', {},
        h('span', { class: 'tag-ico', text: '🏷' }),
        h('a', { href: treeHref(name, tag.sha, ''), text: tag.name }),
        i === 0 ? h('span', { class: 'pill', text: 'Latest' }) : null,
      )),
    ),
    shown.length > limited.length
      ? h('p', { class: 'muted side-more', text: `还有 ${shown.length - limited.length} 个标签` })
      : null,
  )
}

/**
 * 所有仓库页共用的外壳：标题 + tab 导航 + 两栏。
 * build(chrome) 返回左栏内容，右栏由 About / Releases 组成。
 */
async function repoPage(app, ctx, { name, active, ref, crumbs, build }) {
  ctx.setCrumbs(crumbs)
  renderLoading(app, `打开 ${stripGitSuffix(name)}.git …`)

  const chrome = await loadChrome(name)
  const license = await detectRootLicense(chrome.repo, chrome.headSha)
  const description = (await registryDescription(name)) || firstLine(chrome.commit)
  const useRef = ref || chrome.branch
  const left = await build(chrome, useRef)

  mount(app,
    repoHeader({ name, description }),
    repoNav({ name, ref: useRef, active, heads: chrome.heads, tags: chrome.tags }),
    h('div', { class: 'layout' },
      h('div', { class: 'layout-main' }, left),
      h('aside', { class: 'layout-side' },
        aboutPanel({
          name, ref: useRef, description, license,
          heads: chrome.heads, tags: chrome.tags, repo: chrome.repo,
        }),
        releasesPanel({ name, tags: chrome.tags }),
      ),
    ),
  )

  return chrome
}

/* --- 首页 ---------------------------------------------------------------- */

export async function viewHome(app, ctx) {
  ctx.setCrumbs([])
  renderLoading(app, '读取 repository.json…')

  const { repositories, error } = await loadRegistry()

  if (error) {
    mount(app,
      h('h1', 'Repositories'),
      h('p', { class: 'muted' }, '没有读到 repository.json（', error, '）。'),
      h('p', { class: 'muted' }, '把仓库清单放在站点根目录即可。'),
    )
    return
  }

  if (repositories.length === 0) {
    mount(app,
      h('h1', 'Repositories'),
      h('p', { class: 'muted' }, 'repository.json 里还没有任何仓库。'),
    )
    return
  }

  mount(app,
    h('h1', { text: 'Repositories' }),
    h('p', { class: 'muted', text: `${repositories.length} 个只读仓库` }),
    h('div', { style: 'height:16px' }),
    h('ul', { class: 'repo-list' },
      repositories.map(entry => h('li', {},
        h('a', { href: repoHref(entry.name) },
          h('div', { class: 'name', text: `${entry.name}.git` }),
          entry.description ? h('div', { class: 'desc', text: entry.description }) : null,
        ),
      )),
    ),
  )
}

/* --- 仓库概览（Code） ---------------------------------------------------- */

export async function viewRepo(app, ctx, route) {
  const name = route.repo

  await repoPage(app, ctx, {
    name,
    active: 'code',
    crumbs: [{ text: `${stripGitSuffix(name)}.git`, href: repoHref(name) }],
    build: async (chrome, ref) => {
      const entries = await chrome.repo.listDirectory(chrome.headSha, '')
      const docs = findDocs(entries, '')

      return [
        fileBox({ name, ref, commit: chrome.commit, entries }),
        docs.length ? docsPanel(chrome.repo, chrome.headSha, docs) : null,
      ]
    },
  })
}

/* --- 目录 ---------------------------------------------------------------- */

export async function viewTree(app, ctx, route) {
  const name = route.repo
  const ref = route.ref
  const path = route.path

  const crumbs = [{ text: `${stripGitSuffix(name)}.git`, href: repoHref(name) }]
  if (path) crumbs.push({ text: path, href: treeHref(name, ref, path) })

  await repoPage(app, ctx, {
    name, active: 'code', ref, crumbs,
    build: async (chrome) => {
      const sha = await chrome.repo.resolveCommitish(ref || 'HEAD')
      const entries = await chrome.repo.listDirectory(sha, path)
      const docs = findDocs(entries, path)

      return [
        breadcrumbHeader(name, ref, path),
        fileBox({ name, ref, commit: chrome.commit, entries, path }),
        docs.length ? docsPanel(chrome.repo, sha, docs) : null,
      ]
    },
  })
}

/* --- 文件 ---------------------------------------------------------------- */

export async function viewBlob(app, ctx, route) {
  const name = route.repo
  const ref = route.ref
  const path = route.path

  await repoPage(app, ctx, {
    name, active: 'code', ref,
    crumbs: [
      { text: `${stripGitSuffix(name)}.git`, href: repoHref(name) },
      { text: path, href: blobHref(name, ref, path) },
    ],
    build: async (chrome) => {
      const sha = await chrome.repo.resolveCommitish(ref || 'HEAD')
      const { entry, bytes } = await chrome.repo.readFile(sha, path)

      const head = h('div', { class: 'blob-head' },
        h('span', { class: 'blob-path', text: path }),
        h('span', { class: 'blob-meta' },
          h('span', { class: 'muted', text: `${fmtBytes(bytes.length)} · ${entry.mode}` }),
          ' ',
          downloadButton(path, bytes),
        ),
      )

      let body
      if (isImage(path) && bytes.length < 4 * 1024 * 1024) {
        const blob = new Blob([bytes], { type: guessMime(path) })
        body = h('div', { class: 'blob-image' },
          h('img', { src: URL.createObjectURL(blob), alt: path }))
      } else if (looksBinary(path) || bytesLookBinary(bytes)) {
        body = h('div', { class: 'notice binary-note' },
          '二进制文件，不做渲染。大小 ', fmtBytes(bytes.length), '。')
      } else {
        body = h('pre', { class: 'blob' }, numberLines(decodeText(bytes)))
      }

      return [
        breadcrumbHeader(name, ref, path),
        h('div', { class: 'panel' }, head, body),
      ]
    },
  })
}

/* --- 提交历史 ------------------------------------------------------------ */

export async function viewCommits(app, ctx, route) {
  const name = route.repo
  const ref = route.ref

  await repoPage(app, ctx, {
    name, active: 'commits', ref,
    crumbs: [
      { text: `${stripGitSuffix(name)}.git`, href: repoHref(name) },
      { text: '提交' },
    ],
    build: async (chrome) => {
      const sha = await chrome.repo.resolveCommitish(ref || 'HEAD')
      const items = []

      for await (const item of chrome.repo.walkCommits(sha, 60)) {
        items.push(h('li', {},
          h('div', { class: 'commit-body' },
            h('div', { class: 'commit-subject', text: firstLine(item.commit) }),
            h('div', { class: 'commit-meta' },
              h('span', { text: (item.commit.author && item.commit.author.name) || 'unknown' }),
              ' · ',
              fmtDate(item.commit.author && item.commit.author.date),
            ),
          ),
          h('div', { class: 'commit-side' },
            h('code', { class: 'commit-sha', text: shortSha(item.sha) }),
            link(treeHref(name, item.sha, ''), '浏览'),
          ),
        ))
      }

      return [
        breadcrumbHeader(name, ref, ''),
        h('div', { class: 'panel' },
          h('div', { class: 'panel-head' },
            h('span', { class: 'panel-title', text: `最近 ${items.length} 次提交` })),
          items.length
            ? h('ul', { class: 'commit-list' }, items)
            : h('div', { class: 'notice', text: '没有提交' }),
        ),
      ]
    },
  })
}

/* --- 分支 ---------------------------------------------------------------- */

export async function viewBranches(app, ctx, route) {
  const name = route.repo

  await repoPage(app, ctx, {
    name, active: 'branches',
    crumbs: [
      { text: `${stripGitSuffix(name)}.git`, href: repoHref(name) },
      { text: '分支' },
    ],
    build: async (chrome) => {
      const rows = []

      for (const head of chrome.heads) {
        let subject = ''
        let when = ''
        try {
          const c = await chrome.repo.getCommit(head.sha)
          subject = firstLine(c)
          when = fmtDate(c.author && c.author.date)
        } catch {
          subject = '(读取失败)'
        }

        rows.push(h('li', {},
          h('div', { class: 'ref-main' },
            h('div', { class: 'ref-name' },
              link(treeHref(name, head.sha, ''), head.name),
              head.name === chrome.branch ? h('span', { class: 'pill', text: '默认' }) : null,
            ),
            h('div', { class: 'ref-meta' },
              h('code', { text: shortSha(head.sha) }),
              subject ? ` · ${subject}` : '',
            ),
          ),
          h('div', { class: 'ref-side' },
            h('span', { class: 'muted', text: when }),
          ),
        ))
      }

      return [
        h('div', { class: 'panel' },
          h('div', { class: 'panel-head' },
            h('span', { class: 'panel-title', text: `${chrome.heads.length} 个分支` })),
          rows.length
            ? h('ul', { class: 'ref-list' }, rows)
            : h('div', { class: 'notice', text: '没有分支' }),
        ),
      ]
    },
  })
}

/* --- 标签 ---------------------------------------------------------------- */

export async function viewTags(app, ctx, route) {
  const name = route.repo

  await repoPage(app, ctx, {
    name, active: 'tags',
    crumbs: [
      { text: `${stripGitSuffix(name)}.git`, href: repoHref(name) },
      { text: '标签' },
    ],
    build: async (chrome) => {
      const rows = []

      for (const tag of chrome.tags) {
        // peeled 里有条目说明这是附注标签（指向 tag 对象而非 commit）
        const annotated = chrome.repo.peeled.has(`refs/tags/${tag.name}`)
        let subject = ''
        let when = ''
        let target = tag.sha

        try {
          target = await chrome.repo.resolveCommitish(tag.sha)
          const c = await chrome.repo.getCommit(target)
          subject = firstLine(c)
          when = fmtDate(c.author && c.author.date)
        } catch {
          subject = '(读取失败)'
        }

        rows.push(h('li', {},
          h('div', { class: 'ref-main' },
            h('div', { class: 'ref-name' },
              link(treeHref(name, tag.sha, ''), tag.name),
              annotated ? h('span', { class: 'pill pill-muted', text: '附注' }) : null,
            ),
            h('div', { class: 'ref-meta' },
              h('code', { text: shortSha(target) }),
              subject ? ` · ${subject}` : '',
            ),
          ),
          h('div', { class: 'ref-side' },
            h('span', { class: 'muted', text: when }),
          ),
        ))
      }

      return [
        h('div', { class: 'panel' },
          h('div', { class: 'panel-head' },
            h('span', { class: 'panel-title', text: `${chrome.tags.length} 个标签` })),
          rows.length
            ? h('ul', { class: 'ref-list' }, rows)
            : h('div', { class: 'notice', text: '没有标签' }),
        ),
      ]
    },
  })
}

/* --- 片段 ---------------------------------------------------------------- */

function fileBox({ name, ref, commit, entries, path = '' }) {
  return h('div', { class: 'panel' },
    h('div', { class: 'file-toolbar' },
      h('span', { class: 'branch-chip', text: ref }),
      h('span', { class: 'toolbar-commit' },
        link(commitsHref(name, ref), firstLine(commit)),
        h('span', { class: 'muted', text: ` · ${shortSha(commit.sha)}` }),
      ),
      h('span', { class: 'toolbar-right' },
        link(commitsHref(name, ref), '提交历史'),
      ),
    ),
    fileList(name, ref, path, entries),
  )
}

function fileList(name, ref, basePath, entries) {
  if (!entries.length) return h('div', { class: 'notice', text: '空目录' })

  const joinPath = (n) => (basePath ? `${basePath}/${n}` : n)

  return h('ul', { class: 'file-list' },
    entries.map(entry => {
      const isDir = entry.kind === 'tree'
      const target = joinPath(entry.name)
      return h('li', {},
        h('a', { href: isDir ? treeHref(name, ref, target) : blobHref(name, ref, target) },
          h('span', { class: `file-icon${isDir ? ' is-dir' : ''}`, text: isDir ? '▸' : '·' }),
          h('span', { class: isDir ? 'file-name is-dir' : 'file-name', text: entry.name }),
          h('span', { class: 'file-sha', text: shortSha(entry.sha) }),
        ),
      )
    }),
  )
}

/* --- 文档区（README / LICENSE / ...） ------------------------------------ */

/**
 * 根目录里值得单独展示的文件。顺序就是 tab 的默认顺序，README 永远最前。
 * 参照 GitHub 的行为：文件列表下方给一排 tab，默认打开 README。
 */
const DOC_RULES = [
  { re: /^readme(\.|$)/i, kind: 'readme' },
  { re: /^(licen[cs]e|copying|unlicense)(\.|$)/i, kind: 'license' },
  { re: /^changelog(\.|$)/i, kind: 'doc' },
  { re: /^contributing(\.|$)/i, kind: 'doc' },
  { re: /^code_of_conduct(\.|$)/i, kind: 'doc' },
  { re: /^security(\.|$)/i, kind: 'doc' },
  { re: /^governance(\.|$)/i, kind: 'doc' },
  { re: /^citation(\.|$)/i, kind: 'doc' },
  { re: /^authors?(\.|$)/i, kind: 'doc' },
  { re: /^support(\.|$)/i, kind: 'doc' },
]

function docRank(name) {
  for (let i = 0; i < DOC_RULES.length; i++) {
    if (DOC_RULES[i].re.test(name)) return i
  }
  return -1
}

/** 在目录列表里找出可渲染的文档，返回 [{ path, label, markdown }] */
function findDocs(entries, basePath) {
  const prefix = basePath ? `${basePath}/` : ''

  return entries
    .filter(isFileEntry)
    .map(entry => ({ entry, rank: docRank(entry.name) }))
    .filter(hit => hit.rank >= 0)
    .sort((a, b) => a.rank - b.rank || a.entry.name.localeCompare(b.entry.name))
    .map(hit => ({
      path: prefix + hit.entry.name,
      label: hit.entry.name,
      markdown: /\.(md|markdown)$/i.test(hit.entry.name),
    }))
}

function docsPanel(repo, sha, docs) {
  const tabs = h('div', { class: 'docs-tabs' })
  const body = h('div', { class: 'docs-body' })

  const select = async (index) => {
    const buttons = Array.from(tabs.children)
    buttons.forEach((btn, i) => btn.classList.toggle('is-active', i === index))

    const doc = docs[index]
    body.replaceChildren(h('p', { class: 'muted docs-loading', text: `读取 ${doc.label}…` }))

    try {
      const text = await repo.readFileText(sha, doc.path)
      if (doc.markdown) {
        const wrap = h('div', { class: 'readme' })
        wrap.innerHTML = renderMarkdown(text)
        body.replaceChildren(wrap)
      } else {
        body.replaceChildren(h('pre', { class: 'doc-text' }, text))
      }
    } catch (err) {
      body.replaceChildren(h('p', { class: 'muted docs-loading', text: `读取失败：${err.message}` }))
    }
  }

  docs.forEach((doc, i) => {
    tabs.append(h('button', {
      type: 'button',
      class: 'docs-tab',
      text: doc.label,
      onclick: () => select(i),
    }))
  })

  select(0)

  return h('div', { class: 'panel docs-panel' }, tabs, body)
}

function breadcrumbHeader(name, ref, path) {
  const parts = String(path || '').split('/').filter(Boolean)
  const nodes = [link(repoHref(name), `${stripGitSuffix(name)}.git`)]

  let acc = ''
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part
    nodes.push(h('span', { class: 'sep', text: '/' }))
    nodes.push(link(treeHref(name, ref, acc), part))
  }

  return h('div', { class: 'crumbs' }, nodes)
}

function numberLines(text) {
  const lines = text.split('\n')
  const width = String(lines.length).length
  const frag = document.createDocumentFragment()

  lines.forEach((line, i) => {
    frag.append(h('span', { class: 'line-no', text: String(i + 1).padStart(width, ' ') }))
    frag.append(document.createTextNode(line))
    if (i < lines.length - 1) frag.append(document.createTextNode('\n'))
  })

  return frag
}

function decodeText(bytes) {
  return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
}

/**
 * 裸仓库没有工作树，文件内容只存在于 pack 内部，所以下载不能指向
 * <repo>.git/<path>（那里根本没有这个文件），只能把内存里已经解出来的
 * 字节做成 Blob。超过 32 MiB 就不做了，免得把标签页顶爆。
 */
function downloadButton(path, bytes) {
  const fileName = path.split('/').pop()

  if (bytes.length > 32 * 1024 * 1024) {
    return h('span', { class: 'muted', text: '文件过大，无法直接下载' })
  }

  return h('a', {
    class: 'btn',
    href: '#',
    download: fileName,
    text: '下载',
    onclick: (e) => {
      e.preventDefault()
      const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }))
      const a = document.createElement('a')
      a.href = url
      a.download = fileName
      a.click()
      setTimeout(() => URL.revokeObjectURL(url), 2000)
    },
  })
}

function guessMime(path) {
  const ext = (path.split('.').pop() || '').toLowerCase()
  const map = {
    png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
    webp: 'image/webp', avif: 'image/avif', bmp: 'image/bmp',
    svg: 'image/svg+xml', ico: 'image/x-icon',
  }
  return map[ext] || 'application/octet-stream'
}

/* --- 许可证识别 ---------------------------------------------------------- */

/**
 * 常见许可证的特征串。顺序有讲究：GPL 系要在 BSD 系之前判断，
 * BSD-3 要在 BSD-2 之前（BSD-3 多一条 "Neither the name" 子句）。
 */
const LICENSE_RULES = [
  [/Apache License[\s,]*Version 2\.0/i, 'Apache-2.0 license'],
  [/Mozilla Public License[\s,]*Version 2\.0/i, 'MPL-2.0 license'],
  [/GNU AFFERO GENERAL PUBLIC LICENSE[\s\S]{0,400}?Version 3/i, 'AGPL-3.0 license'],
  [/GNU LESSER GENERAL PUBLIC LICENSE[\s\S]{0,400}?Version 3/i, 'LGPL-3.0 license'],
  [/GNU LESSER GENERAL PUBLIC LICENSE[\s\S]{0,400}?Version 2\.1/i, 'LGPL-2.1 license'],
  [/GNU GENERAL PUBLIC LICENSE[\s\S]{0,400}?Version 3/i, 'GPL-3.0 license'],
  [/GNU GENERAL PUBLIC LICENSE[\s\S]{0,400}?Version 2/i, 'GPL-2.0 license'],
  [/Permission is hereby granted, free of charge, to any person obtaining a copy/i, 'MIT license'],
  [/Permission to use, copy, modify, and(\/or)? distribute this software for any purpose/i, 'ISC license'],
  [/Redistribution and use in source and binary forms[\s\S]*?Neither the name/i, 'BSD-3-Clause license'],
  [/Redistribution and use in source and binary forms/i, 'BSD-2-Clause license'],
  [/This is free and unencumbered software released into the public domain/i, 'Unlicense'],
  [/CC0 1\.0 Universal|Creative Commons Legal Code/i, 'CC0-1.0 license'],
  [/DO WHAT THE FUCK YOU WANT TO PUBLIC LICENSE/i, 'WTFPL'],
]

export function identifyLicense(text) {
  const body = String(text || '')
  for (const [re, label] of LICENSE_RULES) {
    if (re.test(body)) return label
  }
  return ''
}

async function detectRootLicense(repo, sha) {
  try {
    const entries = await repo.listDirectory(sha, '')
    const hit = findDocs(entries, '').find(doc => doc.label && /^(licen[cs]e|copying|unlicense)/i.test(doc.label))
    if (!hit) return null

    let text = ''
    try {
      text = (await repo.readFileText(sha, hit.path)).slice(0, 8000)
    } catch {
      /* 读不出来就只显示文件名 */
    }
    return { path: hit.path, label: identifyLicense(text) || hit.label }
  } catch {
    return null
  }
}
