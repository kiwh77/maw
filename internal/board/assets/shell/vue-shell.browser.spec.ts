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
  test('无工程时不显示 stats/toolbar，侧栏无 board/factory/topo', async ({ page }) => {
    await mockBoard(page, {
      projects: [{ id: 'p1', name: 'Demo' }],
      agents: [],
      tasks: [],
      total: 0,
      counts: {},
    })
    await page.goto('/')
    await page.waitForFunction(() => Boolean(document.querySelector('#vnext-nav')))

    await expect(page.locator('#vnext-nav [data-view="board"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="factory"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="topo"]')).toHaveCount(0)
    await expect(page.locator('#vnext-nav [data-view="nodes"]')).toHaveCount(1)
    await expect(page.locator('#vnext-nav [data-view="tasks"]')).toHaveCount(1)

    const chrome = await page.evaluate(() => ({
      stats: document.getElementById('stats'),
      toolbar: document.querySelector('.toolbar'),
    }))
    expect(chrome.stats).toBeNull()
    expect(chrome.toolbar).toBeNull()
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
    await page.route('**/api/nodes**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ nodes: [] }),
      })
    })
    await page.goto('/')
    await page.locator('#f-project').selectOption('p1')
    await expect(page.locator('#stats')).toBeVisible()
    await page.locator('#vnext-nav [data-view="nodes"]').click()
    await expect(page.locator('#stats')).toHaveCount(0)
    await expect(page.locator('.toolbar')).toHaveCount(0)
    await expect(page.locator('#vnext-view')).toBeVisible()
  })
})
