import { RemoteRepo } from './git/repo.js'

/**
 * repository.json 记录有哪些 xxx.git 目录。
 *
 * 之所以要这个小文件：静态托管没有目录列表 API，而 manifest / IPFS /
 * Pages / EdgeOne 都无法告诉你"这个目录下有什么"。用一份清单换取跨平台
 * 通用性，代价是新增仓库时要更新它（prepare-repo 脚本会自动维护）。
 */

const repoCache = new Map()
let registryCache = null

export async function loadRegistry({ refresh = false } = {}) {
  if (registryCache && !refresh) return registryCache

  const url = new URL('repository.json', document.baseURI).href
  try {
    const res = await fetch(url, { cache: 'no-cache' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const data = await res.json()
    const list = Array.isArray(data?.repositories) ? data.repositories : []
    const seen = new Set()
    registryCache = {
      url,
      repositories: list
        .map(normalizeEntry)
        .filter(entry => {
          if (!isValidRepoName(entry.name)) return false
          if (seen.has(entry.name)) return false
          seen.add(entry.name)
          return true
        })
        .sort((a, b) => a.name.localeCompare(b.name)),
    }
  } catch (err) {
    registryCache = { url, repositories: [], error: err.message }
  }
  return registryCache
}

/**
 * 命名规则：
 *   磁盘上的目录一律以 .git 结尾（p2ping.git）
 *   对外标识一律用裸名（p2ping），repository.json 也存裸名
 * 无论 JSON 里写的是哪一种，这里都归一成裸名，顺带挡掉会跑出站点根的写法。
 */
function normalizeEntry(raw) {
  const source = typeof raw === 'string'
    ? raw
    : (raw && typeof raw === 'object' ? String(raw.name || '') : '')

  return {
    name: stripGitSuffix(source),
    description: (raw && typeof raw === 'object' && raw.description)
      ? String(raw.description)
      : '',
  }
}

/** 去掉结尾的 .git（允许写成 xxx.git.git 这种，一并干掉） */
export function stripGitSuffix(name) {
  return String(name == null ? '' : name).trim().replace(/(\.git)+$/i, '')
}

/** 合法仓库名：非空、不是 . / ..、不含路径分隔符 */
export function isValidRepoName(name) {
  const n = stripGitSuffix(name)
  return n !== '' && n !== '.' && n !== '..' && !/[/\\]/.test(n)
}

export function repoBaseUrl(name) {
  return new URL(`${stripGitSuffix(name)}.git/`, document.baseURI).href
}

export function openRepo(name, { onProgress } = {}) {
  if (repoCache.has(name)) return repoCache.get(name)

  const task = (async () => {
    const repo = new RemoteRepo(repoBaseUrl(name), { onProgress })
    await repo.load()
    return repo
  })()

  repoCache.set(name, task)
  task.catch(() => repoCache.delete(name))
  return task
}

export function isCached(name) {
  return repoCache.has(name)
}
