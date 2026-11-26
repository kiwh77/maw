// Vue 整页壳验收：侧栏无 flow、chrome 仅在有工程时显示。
// 运行：npm --prefix tests/browser test -- --grep 'Vue board shell'

import fs from 'node:fs'
import path from 'node:path'

function loadPlaywright() {
  try {
    return require('@playwright/test')
  } catch {
    return require('../../../../tests/browser/node_modules/@playwright/test')
  }
}

const { test, expect } = loadPlaywright()

const ASSETS = path.resolve(__dirname, '..')
const BUNDLE = path.join(ASSETS, 'dist/board-web.js')

async function mockBoard(page, body) {
  await page.route('**/api/board**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })
  await page.route('**/api/events**', (route) => route.abort())
  await page.route('**/assets/board-web.js', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: fs.readFileSync(BUNDLE),
    })
  })
}

test.describe('Vue board shell chrome & nav', () => {
  test('无工程时不显示 stats/toolbar，侧栏置顶工作台但没有 flow 分组', async ({ page }) => {
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }],
      agents: [],
      tasks: [],
      total: 0,
      counts: {},
    })
    await page.goto('/')
    await page.waitForFunction(() => Boolean(document.querySelector('#vnext-nav')))

    // 置顶工作台：唯一进入看板的侧栏入口，且前面没有分组标题（首个子元素就是按钮）。
    await expect(page.locator('#vnext-nav [data-view]').first()).toHaveAttribute('data-view', 'board')
    expect(
      await page.evaluate(() => document.querySelector('#vnext-nav')?.firstElementChild?.tagName),
    ).toBe('BUTTON')
    await expect(page.locator('#vnext-nav [data-view="board"]')).toHaveCount(1)
    // 工坊／拓扑仍不进侧栏，改由选中工程后的 toolbar 进入。
    await expect(page.locator('#vnext-nav [data-view="factory"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="topo"]')).toHaveCount(0)
    // 调度层下线：节点 / 受管策略 / 归档单页不再进侧栏。
    await expect(page.locator('#vnext-nav [data-view="nodes"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="settings"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="tasks"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="projects"]')).toHaveCount(1)

    const chrome = await page.evaluate(() => ({
      stats: document.getElementById('stats'),
      toolbar: document.querySelector('.toolbar'),
    }))
    expect(chrome.stats).toBeNull()
    expect(chrome.toolbar).toBeNull()
  })

  test('置顶工作台回到无 view 参数的默认页，并保留当前工程', async ({ page }) => {
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }, { id: 'p2', name: 'Empty' }],
      agents: [],
      tasks: [{ id: 'T-1', title: 'One', status: 'ready', project_id: 'p1' }],
      total: 1,
      percent: 0,
      counts: { ready: 1 },
    })
    await page.route('**/api/v1/projects/**', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
    )
    await page.goto('/')
    await page.locator('#f-project').selectOption('p1')
    await expect(page.locator('#columns')).toBeVisible()

    // 切到工程视图：看板容器整个卸载，侧栏工作台不再是当前页。
    await page.locator('#vnext-nav [data-view="projects"]').click()
    await expect(page.locator('#columns')).toHaveCount(0)

    // 点工作台回来：地址回到不带 view 参数的默认形态，工程上下文不丢。
    await page.locator('#vnext-nav [data-view="board"]').click()
    await expect(page.locator('#columns')).toBeVisible()
    await expect(page.locator('#columns [data-tid="T-1"]')).toHaveCount(1)
    const url = new URL(page.url())
    expect(url.search).toBe('')
    expect(url.hash).toBe('#p1')
  })

  test('选中工程后显示 chrome，并可切 board', async ({ page }) => {
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }, { id: 'p2', name: 'Empty' }],
      agents: [],
      tasks: [{ id: 'T-1', title: 'One', status: 'ready', project_id: 'p1' }],
      total: 1,
      percent: 0,
      counts: { ready: 1 },
    })
    await page.goto('/')
    await expect(page.locator('#f-project')).toBeVisible()
    await expect(page.locator('#f-project option[data-project="p1"]')).toHaveAttribute('data-has-tasks', '1')
    await expect(page.locator('#f-project option[data-project="p2"]')).toHaveAttribute('data-has-tasks', '0')
    await expect(page.locator('#f-project option[data-project="p1"]')).toContainText('●')
    await expect(page.locator('#f-project option[data-project="p2"]')).not.toContainText('●')
    await page.locator('#f-project').selectOption('p1')
    await expect(page.locator('#stats')).toBeVisible()
    await expect(page.locator('.toolbar')).toBeVisible()
    await expect(page.locator('#view-toggle')).toBeVisible()
    await expect(page.locator('#columns')).toBeVisible()

    // 五项统计必须排在同一行：列数少于项数时最后一项会单独掉到第二行。
    await expect(page.locator('#stats .stat')).toHaveCount(5)
    const statRows = await page.locator('#stats .stat').evaluateAll(els =>
      new Set(els.map(el => Math.round(el.getBoundingClientRect().top))).size)
    expect(statRows).toBe(1)
  })

  test('窄屏统计条折行时保留行间距且不横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 900 })
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }],
      agents: [],
      tasks: [{ id: 'T-1', title: 'One', status: 'ready', project_id: 'p1' }],
      total: 1,
      done: 0,
      active: 0,
      blocked: 0,
      percent: 0,
      counts: { ready: 1 },
    })
    await page.goto('/')
    await page.locator('#f-project').selectOption('p1')
    await expect(page.locator('#stats .stat')).toHaveCount(5)

    const layout = await page.evaluate(() => {
      const stats = document.querySelector('#stats') as HTMLElement
      const items = [...stats.querySelectorAll('.stat')] as HTMLElement[]
      const tops = [...new Set(items.map(el => Math.round(el.getBoundingClientRect().top)))]
      return {
        rows: tops.length,
        rowGap: parseFloat(getComputedStyle(stats).rowGap || '0'),
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      }
    })
    expect(layout.rows).toBe(2)
    // 折行可以（窄屏放不下五列），但行与行之间不能贴在一起。
    expect(layout.rowGap).toBeGreaterThan(0)
    expect(layout.overflow).toBeLessThanOrEqual(0)
  })

  test('切到节点视图后隐藏工程筛选头', async ({ page }) => {
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }],
      agents: [],
      tasks: [{ id: 'T-1', title: 'One', status: 'ready', project_id: 'p1' }],
      total: 1,
      percent: 0,
      counts: { ready: 1 },
    })
    await page.goto('/')
    await page.locator('#f-project').selectOption('p1')
    await expect(page.locator('#stats')).toBeVisible()
    await page.locator('#vnext-nav [data-view="projects"]').click()
    await expect(page.locator('#stats')).toHaveCount(0)
    await expect(page.locator('.toolbar')).toHaveCount(0)
    await expect(page.locator('#vnext-view')).toBeVisible()
  })
})
