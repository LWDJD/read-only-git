/**
 * hash 路由。
 *
 * 静态托管没有服务端重写能力（Arweave 网关也不会把未知路径回落到
 * index.html），所以客户端路由只能用 hash —— hash 部分不会发给服务器。
 *
 * 形态:
 *   #/                      项目列表
 *   #/NAME                  项目概览
 *   #/NAME/tree/REF/PATH    目录
 *   #/NAME/blob/REF/PATH    文件
 *   #/NAME/commits/REF      提交历史
 */

import { stripGitSuffix } from './store.js'

export function parseHash(hash = location.hash) {
  const raw = String(hash || '').replace(/^#\/?/, '')
  const parts = raw.split('/').filter(s => s !== '').map(safeDecode)

  if (parts.length === 0) return { view: 'home' }

  const [rawRepo, kind, ...rest] = parts
  const repo = stripGitSuffix(rawRepo)
  if (!kind) return { view: 'repo', repo }

  if (kind === 'tree' || kind === 'blob') {
    const ref = rest.shift() || ''
    return { view: kind, repo, ref, path: rest.join('/') }
  }
  if (kind === 'commits') {
    return { view: 'commits', repo, ref: rest.shift() || '' }
  }
  if (kind === 'branches') return { view: 'branches', repo }
  if (kind === 'tags') return { view: 'tags', repo }
  return { view: 'notfound', repo, reason: `unknown section: ${kind}` }
}

export function homeHref() {
  return '#/'
}

export function repoHref(repo, kind, ref, path) {
  const segs = [stripGitSuffix(repo)]
  if (kind) {
    segs.push(kind)
    if (ref) segs.push(ref)
    if (path) segs.push(...String(path).split('/').filter(Boolean))
  }
  return '#/' + segs.map(encodeURIComponent).join('/')
}

export function repoBaseHref(repo) { return repoHref(repo) }
export function treeHref(repo, ref, path = '') { return repoHref(repo, 'tree', ref, path) }
export function blobHref(repo, ref, path) { return repoHref(repo, 'blob', ref, path) }
export function commitsHref(repo, ref) { return repoHref(repo, 'commits', ref) }
export function branchesHref(repo) { return repoHref(repo, 'branches') }
export function tagsHref(repo) { return repoHref(repo, 'tags') }

export function onChange(handler) {
  const fire = () => handler(parseHash())
  window.addEventListener('hashchange', fire)
  fire()
}

function safeDecode(s) {
  try { return decodeURIComponent(s) } catch { return s }
}

/**
 * 把仓库名拼成可直接粘给 git 的克隆地址。
 *
 * 不带 .git 后缀，与站点上的目录名一致；git 不要求后缀，
 * 而带后缀的路径会被某些网关拦掉。
 */
export function cloneUrl(repoName) {
  return new URL(stripGitSuffix(repoName), document.baseURI).href
}
