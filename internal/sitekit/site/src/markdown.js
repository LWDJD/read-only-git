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

  // 白名单折叠标签：README 常用 <details>/<summary>，这里把它们从转义态
  // 还原成真标签。只认纯标签（summary 允许带文字），不认任何属性。
  // 其余 HTML 仍是字面文本，脚本照旧进不来；代码块已在上面被占位替换，
  // 里面的字面标签不会被误还原。
  text = text.replace(/&lt;(\/?)summary&gt;/gi, '<$1summary>')
             .replace(/&lt;(\/?)details&gt;/gi, '<$1details>')

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

  const lines = text.split('\n')
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li]
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

    // 折叠块标签自成一块，不包 <p>（浏览器对 <p><details> 容错但不体面）
    if (/^\s*<\/?details>\s*$/i.test(line) || /^\s*<summary>[\s\S]*<\/summary>\s*$/i.test(line)) {
      closeList()
      closeQuote()
      out.push(line.trim())
      continue
    }

    // GFM 表格：首行是表头，次行是 |---| 分隔行，其余是数据行。
    // 不支持表格的后果很直观：README 里的表格全部退化成带竖线的段落。
    if (/^\s*\|/.test(line) && li + 1 < lines.length && isTableDivider(lines[li + 1])) {
      closeList()
      closeQuote()
      const header = parseTableRow(line)
      li++
      const body = []
      while (li + 1 < lines.length && /^\s*\|/.test(lines[li + 1])) {
        li++
        body.push(parseTableRow(lines[li]))
      }
      out.push(renderTable(header, body))
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

/** 切一行表格单元格：去掉首尾的竖线再按竖线切 */
function parseTableRow(line) {
  const s = line.trim().replace(/^\|/, '').replace(/\|$/, '')
  return s.split('|').map(c => c.trim())
}

/** 分隔行：每个单元格都是 :---: 这种写法 */
function isTableDivider(line) {
  const cells = parseTableRow(line)
  return cells.length > 0 && cells.every(c => /^:?-{3,}:?$/.test(c))
}

function renderTable(header, rows) {
  const th = header.map(c => `<th>${inline(c)}</th>`).join('')
  const trs = rows.map(r => {
    // 单元格数不齐时补空，不让一行的错位破坏整张表
    const cells = header.map((_, i) => `<td>${inline(r[i] || '')}</td>`).join('')
    return `<tr>${cells}</tr>`
  }).join('')
  return `<table><thead><tr>${th}</tr></thead><tbody>${trs}</tbody></table>`
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
