import { onChange } from './router.js'
import * as views from './views.js'
import { h, mount } from './ui.js'
import { loadRegistry } from './store.js'

const app = document.getElementById('app')
const crumbsEl = document.getElementById('crumbs')
const footerEl = document.getElementById('footer')

function setCrumbs(items) {
  if (!items || items.length === 0) {
    mount(crumbsEl)
    return
  }
  const nodes = []
  items.forEach((item, i) => {
    if (i > 0) nodes.push(h('span', { class: 'sep', text: '/' }))
    nodes.push(item.href
      ? h('a', { href: item.href, text: item.text })
      : h('span', { text: item.text }))
  })
  mount(crumbsEl, nodes)
}

const ctx = { setCrumbs }

async function route(parsed) {
  window.scrollTo(0, 0)
  try {
    switch (parsed.view) {
      case 'home': return await views.viewHome(app, ctx)
      case 'repo': return await views.viewRepo(app, ctx, parsed)
      case 'tree': return await views.viewTree(app, ctx, parsed)
      case 'blob': return await views.viewBlob(app, ctx, parsed)
      case 'commits': return await views.viewCommits(app, ctx, parsed)
      case 'branches': return await views.viewBranches(app, ctx, parsed)
      case 'tags': return await views.viewTags(app, ctx, parsed)
      default:
        ctx.setCrumbs([])
        return views.renderError(app, new Error(parsed.reason || '未知路由'))
    }
  } catch (err) {
    console.error(err)
    views.renderError(app, err)
  }
}

async function renderFooter() {
  const { repositories, error } = await loadRegistry()
  const text = ['read-only-git · 静态托管的只读 Git 浏览器']
  if (!error && repositories.length) text.push(` · ${repositories.length} 个仓库`)
  mount(footerEl, h('div', {}, text.join('')))
}

renderFooter()
onChange(route)
