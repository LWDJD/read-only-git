/**
 * 图标库：手绘 SVG，描边风格，16 网格。
 *
 * 为什么自己画：此前图标是 unicode 字符（⬢ ▸ 🔗 ⭐），不同平台渲染
 * 完全不一样，emoji 一出现界面就显得廉价。SVG 用 currentColor 跟随
 * 主题变色，深浅色都不用单独处理。
 *
 * 画法约定：viewBox 统一 0 0 16 16，内容留 1~2px 边距；描边不填充，
 * stroke 宽度与圆角在 style.css 的 .icon 里统一给，形状本身只管路径。
 */

const NS = 'http://www.w3.org/2000/svg'

// paths 是描边路径，circles 是 [cx, cy, r]。
const ICONS = {
  // 折角的纸，通用文件
  file: {
    paths: [
      'M3.5 2.5a.75.75 0 0 1 .75-.75h5L12.5 5.25V13a.75.75 0 0 1-.75.75h-7.5a.75.75 0 0 1-.75-.75Z',
      'M9 2v3.5h3.5',
    ],
  },

  // 带横线的文档（README 之类）
  doc: {
    paths: [
      'M3.5 2.5a.75.75 0 0 1 .75-.75h5L12.5 5.25V13a.75.75 0 0 1-.75.75h-7.5a.75.75 0 0 1-.75-.75Z',
      'M9 2v3.5h3.5',
      'M5.5 8h5M5.5 10h5M5.5 12h3',
    ],
  },

  // 文件夹
  dir: {
    paths: [
      'M1.75 3.75A.75.75 0 0 1 2.5 3h3.2L7.25 4.75H13.5a.75.75 0 0 1 .75.75v6.75a.75.75 0 0 1-.75.75H2.5a.75.75 0 0 1-.75-.75Z',
    ],
  },

  // 立着的书，仓库标识
  repo: {
    paths: [
      'M3.5 2.5A1.25 1.25 0 0 1 4.75 1.25H13v10.5H4.75A1.25 1.25 0 0 0 3.5 13Z',
      'M3.5 2.5V13',
    ],
  },

  // 文件 + M 脚标
  markdown: {
    paths: [
      'M3.5 2.5a.75.75 0 0 1 .75-.75h5L12.5 5.25V13a.75.75 0 0 1-.75.75h-7.5a.75.75 0 0 1-.75-.75Z',
      'M9 2v3.5h3.5',
      'M5 12.25V9.5l1.4 1.4L7.8 9.5v2.75',
    ],
  },

  // 文件 + 尖括号
  code: {
    paths: [
      'M3.5 2.5a.75.75 0 0 1 .75-.75h5L12.5 5.25V13a.75.75 0 0 1-.75.75h-7.5a.75.75 0 0 1-.75-.75Z',
      'M9 2v3.5h3.5',
      'M6.4 8.9 5.15 10.15 6.4 11.4M9.6 8.9l1.25 1.25L9.6 11.4',
    ],
  },

  // 相框里的山与太阳
  image: {
    paths: [
      'M2 3.25a.75.75 0 0 1 .75-.75h10.5a.75.75 0 0 1 .75.75v9.5a.75.75 0 0 1-.75.75H2.75a.75.75 0 0 1-.75-.75Z',
      'M2 11.5l3.25-3.25 2.25 2.25 2-2L14 13',
    ],
    circles: [[5.25, 6, 1]],
  },

  // 芯片，二进制件
  binary: {
    paths: [
      'M4.5 4.5h7v7h-7Z',
      'M6 2.25v2.25M8 2.25v2.25M10 2.25v2.25M6 11.5v2.25M8 11.5v2.25M10 11.5v2.25',
      'M2.25 6h2.25M2.25 8h2.25M2.25 10h2.25M11.5 6h2.25M11.5 8h2.25M11.5 10h2.25',
    ],
  },

  // 拉链盒，压缩包
  archive: {
    paths: [
      'M3.25 2.5a.5.5 0 0 1 .5-.5h8.5a.5.5 0 0 1 .5.5v11a.5.5 0 0 1-.5.5h-8.5a.5.5 0 0 1-.5-.5Z',
      'M8 2v2M8 5.25v2M8 8.5v2',
    ],
  },

  // 盾与勾，许可证
  license: {
    paths: [
      'M8 1.75 13 3.5v3.75c0 3.25-2.2 5.7-5 6.75-2.8-1.05-5-3.5-5-6.75V3.5Z',
      'M6 7.75 7.4 9.15 10.25 6.3',
    ],
  },

  // 下箭头入托盘
  download: {
    paths: [
      'M8 2.5v7M5.25 7 8 9.75 10.75 7',
      'M2.75 11.25v1.5a.75.75 0 0 0 .75.75h9a.75.75 0 0 0 .75-.75v-1.5',
    ],
  },

  // 错位的两张纸
  copy: {
    paths: [
      'M5.75 5.5h6.5a.75.75 0 0 1 .75.75v6.5a.75.75 0 0 1-.75.75h-6.5a.75.75 0 0 1-.75-.75v-6.5a.75.75 0 0 1 .75-.75Z',
      'M3.5 10.5h-.25a.75.75 0 0 1-.75-.75V3.25a.75.75 0 0 1 .75-.75H9.5a.75.75 0 0 1 .75.75v.25',
    ],
  },

  // 两叉的枝
  branch: {
    paths: [
      'M4.5 4.75v6',
      'M4.5 8.5h4.25A2.25 2.25 0 0 0 11 6.25V5.4',
    ],
    circles: [[4.5, 3.5, 1.15], [4.5, 12, 1.15], [11, 4.15, 1.15]],
  },

  // 吊牌
  tag: {
    paths: [
      'M8.25 2.25h5.5v5.5l-6 6a1 1 0 0 1-1.4 0L2.5 9.9a1 1 0 0 1 0-1.4Z',
    ],
    circles: [[11.25, 4.75, 0.85]],
  },

  // 横线穿圆，提交
  commit: {
    paths: ['M1.75 8h3M11.25 8h3'],
    circles: [[8, 8, 2.15]],
  },

  // 两环相扣，链接
  link: {
    paths: [
      'M6.75 9.25 9.25 6.75',
      'M7.5 4.75 9 3.25a2.3 2.3 0 0 1 3.25 3.25L10.75 8',
      'M8.5 11.25 7 12.75a2.3 2.3 0 0 1-3.25-3.25L5.25 8',
    ],
  },

  // 五角星
  star: {
    paths: [
      'M8 1.9l1.75 3.55 3.9.57-2.83 2.75.67 3.88L8 10.77l-3.49 1.83.67-3.88-2.83-2.75 3.9-.57Z',
    ],
  },

  // 逆时针回旋的钟
  history: {
    paths: [
      'M2.75 8a5.25 5.25 0 1 0 1.55-3.72',
      'M2.25 2.75v2.75h2.75',
      'M8 5.25V8l2 1.15',
    ],
  },

  // 方框外箭头
  extlink: {
    paths: [
      'M9.5 2.5h4v4',
      'M13.5 2.5 8.25 7.75',
      'M12.25 9.5v3.25a.75.75 0 0 1-.75.75H3.5a.75.75 0 0 1-.75-.75V4.5a.75.75 0 0 1 .75-.75h3.25',
    ],
  },
}

/**
 * 造一个图标节点。
 *
 * 形状走 createElementNS（SVG 不认 createElement），描边样式统一由
 * style.css 的 .icon 给，这里不写任何样式属性。
 */
export function icon(name, cls) {
  const spec = ICONS[name] || ICONS.file
  const svg = document.createElementNS(NS, 'svg')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('class', cls ? `icon ${cls}` : 'icon')
  svg.setAttribute('aria-hidden', 'true')

  for (const d of spec.paths || []) {
    const p = document.createElementNS(NS, 'path')
    p.setAttribute('d', d)
    svg.append(p)
  }
  for (const c of spec.circles || []) {
    const el = document.createElementNS(NS, 'circle')
    el.setAttribute('cx', String(c[0]))
    el.setAttribute('cy', String(c[1]))
    el.setAttribute('r', String(c[2]))
    svg.append(el)
  }
  return svg
}

/* 文件类型 → 图标名。宁可保守：认不出就用通用文件，不猜。 */
const CODE_EXT = new Set([
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'rs', 'go', 'py', 'rb', 'java',
  'c', 'h', 'cpp', 'hpp', 'cc', 'cs', 'php', 'sh', 'bash', 'zsh', 'fish',
  'json', 'yml', 'yaml', 'toml', 'ini', 'cfg', 'conf', 'sql', 'xml', 'css',
  'scss', 'less', 'html', 'htm', 'vue', 'svelte', 'lua', 'zig', 'nim', 'pl',
])
const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg'])
const ARCHIVE_EXT = new Set(['zip', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'tar'])
const BINARY_EXT = new Set(['wasm', 'so', 'dll', 'exe', 'bin', 'o', 'a', 'class', 'jar', 'pyc'])

export function iconForPath(path, kind) {
  if (kind === 'tree' || kind === 'dir') return 'dir'

  const name = String(path || '').split('/').pop() || ''
  if (/^(licen[cs]e|copying|unlicense)(\.|$)/i.test(name)) return 'license'
  if (/^readme(\.|$)/i.test(name)) return /\.(md|markdown)$/i.test(name) ? 'markdown' : 'doc'

  const ext = (name.split('.').pop() || '').toLowerCase()
  if (ext === 'md' || ext === 'markdown') return 'markdown'
  if (IMAGE_EXT.has(ext)) return 'image'
  if (ARCHIVE_EXT.has(ext)) return 'archive'
  if (BINARY_EXT.has(ext)) return 'binary'
  if (CODE_EXT.has(ext)) return 'code'
  return 'file'
}
