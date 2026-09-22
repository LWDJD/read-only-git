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
