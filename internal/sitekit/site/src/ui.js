/** 极小的 DOM 构造器，避免引入框架。 */
export function h(tag, props, ...children) {
  const el = document.createElement(tag)

  if (props && typeof props === 'object' && !(props instanceof Node) && !Array.isArray(props)) {
    for (const [key, value] of Object.entries(props)) {
      if (value === null || value === undefined || value === false) continue
      if (key === 'class') el.className = value
      else if (key === 'text') el.textContent = value
      else if (key === 'html') el.innerHTML = value
      else if (key === 'dataset') Object.assign(el.dataset, value)
      else if (key.startsWith('on') && typeof value === 'function') {
        el.addEventListener(key.slice(2).toLowerCase(), value)
      } else el.setAttribute(key, value === true ? '' : String(value))
    }
  } else if (props !== undefined && props !== null) {
    children.unshift(props)
  }

  append(el, children)
  return el
}

function append(el, children) {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue
    if (Array.isArray(child)) append(el, child)
    else if (child instanceof Node) el.append(child)
    else el.append(document.createTextNode(String(child)))
  }
}

export function mount(el, ...children) {
  el.replaceChildren()
  append(el, children)
  return el
}

export function fmtDate(date) {
  if (!(date instanceof Date) || Number.isNaN(date.getTime())) return ''
  const pad = n => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
    + ` ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function fmtBytes(n) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}

export function shortSha(sha) {
  return String(sha || '').slice(0, 8)
}

/**
 * 相对时间：刚刚 / N 分钟前 / N 天前……
 *
 * 列表里绝对时间没有距离感，但精确时间仍有用，
 * 调用方通常把 fmtDate 挂在 title 上，两个都要。
 */
export function fmtRelative(date) {
  if (!(date instanceof Date) || Number.isNaN(date.getTime())) return ''
  const min = Math.floor((Date.now() - date.getTime()) / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min} 分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时前`
  const day = Math.floor(hr / 24)
  if (day < 30) return `${day} 天前`
  const mon = Math.floor(day / 30)
  if (mon < 12) return `${mon} 个月前`
  return `${Math.floor(mon / 12)} 年前`
}

/**
 * 提交者头像：名字首字母的色块，颜色由 email 稳定散列。
 *
 * 只是视觉锚点，不是身份验证。饱和度与亮度取固定值，
 * 白字在任何色相上都够对比，深浅色主题都不用单独处理。
 */
export function avatarNode(name, email) {
  const key = String(email || name || '?').trim().toLowerCase()
  let hval = 0
  for (let i = 0; i < key.length; i++) hval = (hval * 31 + key.charCodeAt(i)) >>> 0
  const label = String(name || '?').trim().slice(0, 1).toUpperCase() || '?'
  return h('span', {
    class: 'avatar',
    style: `background:hsl(${hval % 360},52%,40%)`,
    text: label,
  })
}

/**
 * 加载骨架：几条会呼吸的灰条，替代「加载中…」一行字。
 *
 * 动画只是透明度起伏，不闪光不位移：它是状态反馈，不是装饰。
 */
export function skeletonLines(n, width) {
  const out = []
  for (let i = 0; i < n; i++) {
    out.push(h('div', {
      class: 'sk sk-line',
      style: width ? `width:${typeof width === 'function' ? width(i) : width}` : '',
    }))
  }
  return out
}

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|avif|bmp|ico|svg)$/i
const BINARY_EXT = /\.(png|jpe?g|gif|webp|avif|bmp|ico|pdf|zip|gz|tgz|bz2|xz|7z|rar|wasm|so|dll|exe|bin|o|a|class|jar|mp3|mp4|mov|avi|woff2?|ttf|otf)$/i

export function isImage(path) { return IMAGE_EXT.test(path) }
export function looksBinary(path) { return BINARY_EXT.test(path) }

/** 判断字节内容是否像二进制（含 NUL 或大量不可打印字符） */
export function bytesLookBinary(bytes) {
  const n = Math.min(bytes.length, 8000)
  if (n === 0) return false
  let suspicious = 0
  for (let i = 0; i < n; i++) {
    const b = bytes[i]
    if (b === 0) return true
    if (b < 9 || (b > 13 && b < 32)) suspicious++
  }
  return suspicious / n > 0.1
}

export function detectLanguage(path) {
  const ext = (path.split('.').pop() || '').toLowerCase()
  const map = {
    js: 'javascript', mjs: 'javascript', cjs: 'javascript', ts: 'typescript',
    tsx: 'tsx', jsx: 'jsx', rs: 'rust', go: 'go', py: 'python', rb: 'ruby',
    java: 'java', c: 'c', h: 'c', cpp: 'cpp', hpp: 'cpp', cc: 'cpp',
    cs: 'csharp', php: 'php', sh: 'bash', bash: 'bash', zsh: 'bash',
    json: 'json', yml: 'yaml', yaml: 'yaml', toml: 'toml', ini: 'ini',
    md: 'markdown', html: 'html', htm: 'html', css: 'css', scss: 'scss',
    sql: 'sql', xml: 'xml', toml: 'toml',
  }
  return map[ext] || 'text'
}
