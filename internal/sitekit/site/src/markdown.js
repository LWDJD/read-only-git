/**
 * 极简 Markdown 渲染器（零依赖）。
 *
 * 安全性：先把整段文本做 HTML 转义，再套用 markdown 规则生成标签，
 * 因此仓库里的原始 HTML 不会被执行。链接额外过滤协议，挡住
 * javascript: 之类的伪协议。
 */

const ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }

function escapeHtml(input) {
  return String(input).replace(/[&<>"']/g, c => ESCAPES[c])
}

function safeUrl(url) {
  const u = String(url).trim()
  if (/^(https?:|mailto:|#|\/|\.\/|\.\.\/)/i.test(u)) return u
  return '#'
}

export function renderMarkdown(source) {
  if (!source) return ''

  const codeBlocks = []
  let text = escapeHtml(source).replace(/\r\n?/g, '\n')

  // 围栏代码块
  text = text.replace(/```([^\n`]*)\n([\s\S]*?)```/g, (_, lang, body) => {
    const clean = body.replace(/\n$/, '')
    codeBlocks.push(`<pre><code data-lang="${escapeHtml(lang.trim())}">${clean}</code></pre>`)
    return `\n\u0000CB${codeBlocks.length - 1}\u0000\n`
  })

  const out = []
  let listType = null
  let inQuote = false

  const closeList = () => {
    if (listType) {
      out.push(`</${listType}>`)
      listType = null
    }
  }
  const closeQuote = () => {
    if (inQuote) {
      out.push('</blockquote>')
      inQuote = false
    }
  }

  for (const line of text.split('\n')) {
    const marker = /^\u0000CB(\d+)\u0000$/.exec(line.trim())
    if (marker) {
      closeList()
      closeQuote()
      out.push(codeBlocks[Number(marker[1])])
      continue
    }

    if (!line.trim()) {
      closeList()
      closeQuote()
      continue
    }

    let m = /^(#{1,6})\s+(.*)$/.exec(line)
    if (m) {
      closeList()
      closeQuote()
      const level = m[1].length
      out.push(`<h${level}>${inline(m[2])}</h${level}>`)
      continue
    }

    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      closeList()
      closeQuote()
      out.push('<hr>')
      continue
    }

    m = /^&gt;\s?(.*)$/.exec(line)
    if (m) {
      closeList()
      if (!inQuote) {
        out.push('<blockquote>')
        inQuote = true
      }
      out.push(`<p>${inline(m[1])}</p>`)
      continue
    }
    closeQuote()

    m = /^\s*[-*+]\s+(.*)$/.exec(line)
    if (m) {
      if (listType !== 'ul') {
        closeList()
        out.push('<ul>')
        listType = 'ul'
      }
      out.push(`<li>${inline(m[1])}</li>`)
      continue
    }

    m = /^\s*\d+[.)]\s+(.*)$/.exec(line)
    if (m) {
      if (listType !== 'ol') {
        closeList()
        out.push('<ol>')
        listType = 'ol'
      }
      out.push(`<li>${inline(m[1])}</li>`)
      continue
    }

    closeList()
    out.push(`<p>${inline(line)}</p>`)
  }

  closeList()
  closeQuote()
  return out.join('\n')
}

function inline(input) {
  return input
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_, alt, src) => `<img alt="${alt}" src="${safeUrl(src)}">`)
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label, href) => `<a href="${safeUrl(href)}" rel="noopener">${label}</a>`)
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^\w*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
    .replace(/~~([^~]+)~~/g, '<del>$1</del>')
}

/** 去掉 markdown 语法，用于标题或摘要 */
export function stripMarkdown(source) {
  return String(source || '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[#*_>`~-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}
