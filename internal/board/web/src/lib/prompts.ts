/** 看板接入提示词（从 legacy board-app.js 迁入）。 */

export type PromptTask = {
  id?: string
  title?: string
  status?: string
  priority?: number | string
  merge_order?: number | string
  deps?: string[]
  owner?: string
  worktree?: string
  branch?: string
  blocked_reason?: string
  write_scope?: string[]
  read_scope?: string[]
  acceptance?: string[]
  project_id?: string
  project_name?: string
}

export type PromptProject = {
  id?: string
  name?: string
  path?: string
  main_branch?: string
}

export type PromptAgent = {
  id?: string
  task_id?: string
}

function fmtBullet(arr: string[] | undefined, empty: string) {
  if (!arr || !arr.length) return empty
  return arr.map((x) => '- ' + x).join('\n')
}

function takenAgentNames(agents: PromptAgent[] = [], tasks: PromptTask[] = []) {
  const seen = new Set<string>()
  agents.forEach((a) => {
    if (a.id) seen.add(a.id)
  })
  tasks.forEach((t) => {
    if (t.owner) seen.add(t.owner)
  })
  return [...seen].sort()
}

function statusLabel(st: string | undefined, lang: string) {
  const zh = lang !== 'en'
  const map: Record<string, [string, string]> = {
    ready: ['Ready', '可领取'],
    claimed: ['Claimed', '已领取'],
    in_progress: ['In progress', '进行中'],
    self_check: ['Self-check', '自检'],
    in_review: ['In review', '评审中'],
    merging: ['Merging', '合入中'],
    blocked: ['Blocked', '阻塞'],
    conflict: ['Conflict', '冲突'],
    done: ['Done', '完成'],
  }
  const pair = map[String(st || '')]
  if (!pair) return st || '—'
  return zh ? pair[1] : pair[0]
}

export function buildTaskPrompt(
  task: PromptTask,
  project: PromptProject | null | undefined,
  opts: { agents?: PromptAgent[]; tasks?: PromptTask[]; lang?: string } = {},
) {
  const lang = opts.lang === 'en' ? 'en' : 'zh'
  const p = project || {}
  const projName = task.project_name || p.name || task.project_id || (lang === 'en' ? 'unknown project' : '未知工程')
  const projPath = p.path || (lang === 'en' ? '<project path>' : '<工程路径>')
  const mainBranch = p.main_branch || 'main'
  const taken = takenAgentNames(opts.agents, opts.tasks)
  const lines: string[] = []
  if (lang === 'en') {
    lines.push(
      'You are an execution Agent for maw (Multi-Agent Workspace CLI). Connect using the project/task info below and follow the protocol; do not skip steps.',
    )
  } else {
    lines.push(
      '你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请严格按下列工程与任务信息连接 maw 并完成工作，不要跳过协议步骤。',
    )
  }
  lines.push('')
  lines.push(lang === 'en' ? '## Project' : '## 工程信息')
  lines.push((lang === 'en' ? '- Name: ' : '- 名称：') + projName)
  lines.push((lang === 'en' ? '- Project ID: ' : '- 工程 ID：') + (task.project_id || p.id || '—'))
  lines.push((lang === 'en' ? '- Local path: ' : '- 本地路径：') + projPath)
  lines.push((lang === 'en' ? '- Trunk branch: ' : '- 主干分支：') + mainBranch)
  lines.push('')
  lines.push(lang === 'en' ? '## Target task' : '## 目标任务')
  lines.push((lang === 'en' ? '- Task ID: ' : '- 任务 ID：') + task.id)
  lines.push((lang === 'en' ? '- Title: ' : '- 标题：') + (task.title || '—'))
  lines.push(
    (lang === 'en' ? '- Status: ' : '- 状态：') +
      statusLabel(task.status, lang) +
      '（' +
      (task.status || '—') +
      '）',
  )
  lines.push(
    (lang === 'en' ? '- Priority: ' : '- 优先级：') +
      (task.priority ?? '—') +
      (lang === 'en' ? ' · merge_order: ' : ' · merge_order：') +
      (task.merge_order ?? '—'),
  )
  lines.push(
    (lang === 'en' ? '- Deps: ' : '- 依赖：') +
      (task.deps && task.deps.length ? task.deps.join(', ') : lang === 'en' ? 'none' : '无'),
  )
  if (task.owner) lines.push((lang === 'en' ? '- Current owner: ' : '- 当前负责人：') + task.owner)
  if (task.worktree) {
    lines.push((lang === 'en' ? '- Worktree (repo-relative): ' : '- Worktree（相对仓库）：') + task.worktree)
  }
  if (task.branch) lines.push((lang === 'en' ? '- Task branch: ' : '- 任务分支：') + task.branch)
  if (task.blocked_reason) {
    lines.push((lang === 'en' ? '- Blocked reason: ' : '- 阻塞原因：') + task.blocked_reason)
  }
  lines.push(lang === 'en' ? '- write_scope (only these):' : '- 可写范围 write_scope（只改这些）：')
  lines.push(
    fmtBullet(
      task.write_scope,
      lang === 'en'
        ? '- (empty; run maw task show ' + task.id + ' to confirm)'
        : '- （空；先运行 maw task show ' + task.id + ' 确认）',
    ),
  )
  lines.push(lang === 'en' ? '- read_scope:' : '- 只读范围 read_scope：')
  lines.push(fmtBullet(task.read_scope, lang === 'en' ? '- (none)' : '- （无）'))
  lines.push(lang === 'en' ? '- acceptance:' : '- 验收标准 acceptance：')
  lines.push(
    fmtBullet(
      task.acceptance,
      lang === 'en' ? '- (none; use the task card in the worktree)' : '- （无；以 worktree 内任务卡为准）',
    ),
  )
  lines.push('')
  lines.push(lang === 'en' ? '## Join & execute' : '## 接入与执行步骤')
  lines.push(lang === 'en' ? '1. cd into the project:' : '1. 终端进入工程目录：')
  lines.push('   cd ' + projPath)
  const active = ['in_progress', 'claimed', 'self_check', 'conflict'].includes(String(task.status || ''))
  if (active) {
    if (lang === 'en') {
      lines.push('2. If continuing the same owner, reuse that Agent name; otherwise pick a unique unused name from the board.')
      if (task.owner) {
        lines.push(
          '   Current owner: ' + task.owner + (task.worktree ? '; worktree: ' + task.worktree : ''),
        )
      }
      lines.push('   export MAW_AGENT=<unique-name>')
      lines.push(
        '3. If the task already has a worktree: enter it and keep coding; re-bind with maw next if needed (same agent is idempotent).',
      )
      lines.push(
        '   ' +
          (task.worktree
            ? 'cd ' + String(projPath).replace(/\\/g, '/') + '/' + task.worktree
            : 'maw next'),
      )
      lines.push("4. Keep task " + task.id + "; do not touch other agents' worktrees.")
    } else {
      lines.push('2. 若你接续同一负责人，复用其 Agent 名；否则另选看板未占用的唯一名。')
      if (task.owner) {
        lines.push(
          '   当前负责人：' + task.owner + (task.worktree ? '；worktree：' + task.worktree : ''),
        )
      }
      lines.push('   export MAW_AGENT=<唯一名>')
      lines.push('3. 若任务已有 worktree：直接进入该目录继续改代码；需要重新绑定可再执行 maw next（同名 agent 幂等）。')
      lines.push(
        '   ' +
          (task.worktree
            ? 'cd ' + String(projPath).replace(/\\/g, '/') + '/' + task.worktree
            : 'maw next'),
      )
      lines.push('4. 目标任务保持为 ' + task.id + '；勿改其他 agent 的 worktree。')
    }
  } else if (lang === 'en') {
    lines.push(
      '2. Set a unique Agent name for this session (must not collide' +
        (taken.length ? '; taken: ' + taken.join(', ') : '') +
        '):',
    )
    lines.push('   export MAW_AGENT=<unique-name>')
    lines.push('3. Register and claim the next ready task (by priority / merge_order):')
    lines.push('   maw register && maw next')
    lines.push('   or: maw next --agent <unique-name>')
    lines.push('4. Check the claimed task ID. Expected: ' + task.id + ' (' + (task.title || '') + ').')
    lines.push('   - If it matches: work in the printed worktree.')
    lines.push(
      "   - If another task: continue with what you got (queue order), or re-check the board; do not edit others' worktrees.",
    )
  } else {
    lines.push(
      '2. 设定本会话唯一 Agent 名（勿与已注册冲突' +
        (taken.length ? '；已占用：' + taken.join(', ') : '') +
        '）：',
    )
    lines.push('   export MAW_AGENT=<唯一名>')
    lines.push('3. 注册并领取下一道 ready 任务（按 priority / merge_order）：')
    lines.push('   maw register && maw next')
    lines.push('   或：maw next --agent <唯一名>')
    lines.push('4. 核对输出的任务 ID。期望领取：' + task.id + '（' + (task.title || '') + '）。')
    lines.push('   - 若正是该任务：进入输出的 worktree 目录工作。')
    lines.push(
      '   - 若领到其他任务：以实际领到的任务为准继续（队列按优先级）；或查看看板后再调度，勿改他人 worktree。',
    )
  }
  if (lang === 'en') {
    lines.push(
      '5. Only modify inside the maw worktree; only write_scope; run maw request-change before going out of scope.',
    )
    lines.push('6. Milestones: maw progress -m "…"; finish: maw submit; stuck: maw block.')
    lines.push('7. After submit you may maw merge --yes; do not git merge/rebase/push trunk yourself.')
    lines.push('8. After merge, if ready tasks remain, keep calling maw next until none left or the user stops you.')
    lines.push('9. Board: maw board --serve (default http://127.0.0.1:7420/).')
    lines.push('')
    lines.push('## Protocol red lines')
    lines.push('- DB ~/.maw/maw.db; work only in maw worktrees; merge with maw merge --yes.')
    lines.push('- Before handoff: maw handoff; unregister only when no more claimable tasks.')
    lines.push("- On errors, read maw's Next hint first; do not bypass.")
    lines.push('- Before starting: maw task show ' + task.id)
  } else {
    lines.push('5. 只在 maw 创建的 worktree 中修改；只改 write_scope；越界前运行 maw request-change。')
    lines.push('6. 里程碑：maw progress -m \"说明\"；完成：maw submit；阻塞：maw block（按 CLI 提示附原因）。')
    lines.push('7. 完成 submit 后可自行合入：maw merge --yes；禁止直接 git merge / rebase / push 主干。')
    lines.push('8. 合入后若仍有 ready 任务，继续 maw next 领取并执行下一题，循环直至无题可领或用户叫停。')
    lines.push('9. 全局看板：maw board --serve（默认 http://127.0.0.1:7420/）。')
    lines.push('')
    lines.push('## 协议红线')
    lines.push('- 状态库在本机 ~/.maw/maw.db；本仓库 .multiagent/ 仅存配置与蓝图。')
    lines.push('- 换人或中断前：maw handoff；本会话无更多可领任务时再 maw unregister。')
    lines.push('- 出错时先读 maw 输出的「下一步」提示，不要绕过。')
    lines.push('- 开始前建议：maw task show ' + task.id)
  }
  return lines.join('\n')
}

export function buildProjectJoinPrompt(
  project: PromptProject | null | undefined,
  opts: { agents?: PromptAgent[]; tasks?: PromptTask[]; lang?: string } = {},
) {
  const lang = opts.lang === 'en' ? 'en' : 'zh'
  if (!project || !project.id) {
    return lang === 'en'
      ? 'No projects on this board yet. In the target git repo run: maw init && maw plan apply .multiagent/blueprint.toml --yes'
      : '当前看板尚无已接入工程。请先在目标 git 仓库内运行：maw init && maw plan apply .multiagent/blueprint.toml --yes'
  }
  const taken = takenAgentNames(opts.agents, opts.tasks)
  const ready = (opts.tasks || []).filter(
    (t) => t.status === 'ready' && (!project.id || t.project_id === project.id),
  )
  const lines: string[] = []
  if (lang === 'en') {
    lines.push(
      'You are an execution Agent for maw (Multi-Agent Workspace CLI). Connect to the project below, claim and finish the next ready task.',
    )
  } else {
    lines.push(
      '你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请连接下列工程，领取并完成下一道可领取（ready）任务。',
    )
  }
  lines.push('')
  lines.push(lang === 'en' ? '## Project' : '## 工程信息')
  lines.push((lang === 'en' ? '- Name: ' : '- 名称：') + (project.name || '—'))
  lines.push((lang === 'en' ? '- Project ID: ' : '- 工程 ID：') + (project.id || '—'))
  lines.push((lang === 'en' ? '- Local path: ' : '- 本地路径：') + (project.path || '—'))
  lines.push((lang === 'en' ? '- Trunk branch: ' : '- 主干分支：') + (project.main_branch || 'main'))
  lines.push('')
  lines.push(
    lang === 'en'
      ? '## Ready tasks (reference only; maw next is authoritative)'
      : '## 当前可领取任务（供参考，以 maw next 实际领取为准）',
  )
  if (ready.length) {
    ready.slice(0, 8).forEach((t) => {
      lines.push('- ' + t.id + '：' + (t.title || '') + '（priority=' + (t.priority ?? '?') + '）')
    })
    if (ready.length > 8) {
      lines.push((lang === 'en' ? '- …' : '- …共 ') + ready.length + (lang === 'en' ? ' total' : ' 道'))
    }
  } else {
    lines.push(lang === 'en' ? '- (no ready under current filter)' : '- （当前筛选下无 ready；先看看板或等待依赖完成）')
  }
  lines.push('')
  lines.push(lang === 'en' ? '## Join & execute' : '## 接入与执行步骤')
  lines.push('1. cd ' + (project.path || (lang === 'en' ? '<project path>' : '<工程路径>')))
  lines.push(
    '2. export MAW_AGENT=' +
      (lang === 'en' ? '<unique-name>' : '<唯一名>') +
      (taken.length ? (lang === 'en' ? '  # taken: ' : '  # 已占用：') + taken.join(', ') : ''),
  )
  lines.push('3. maw register && maw next')
  lines.push(
    lang === 'en'
      ? '4. Enter the printed worktree; only that task\'s write_scope; maw progress / maw submit; then maw merge --yes if appropriate.'
      : '4. 进入输出的 worktree，只改该任务 write_scope；maw progress / maw submit；完成后可自行 maw merge --yes。',
  )
  lines.push(
    lang === 'en'
      ? '5. After merge, if ready remain, keep maw next until none left.'
      : '5. 合入后若仍有 ready，继续 maw next 领下一题并执行，循环直至无题可领。',
  )
  lines.push(lang === 'en' ? '6. Board: maw board --serve' : '6. 看板：maw board --serve')
  lines.push('')
  lines.push(lang === 'en' ? '## Protocol red lines' : '## 协议红线')
  lines.push(
    lang === 'en'
      ? '- DB ~/.maw/maw.db; work only in maw worktrees; merge with maw merge --yes.'
      : '- 状态库 ~/.maw/maw.db；只在 maw worktree 内工作；合入用 maw merge --yes（可自行执行）。',
  )
  lines.push(lang === 'en' ? "- On errors, read maw's Next hint first." : '- 出错先读 maw「下一步」提示。')
  return lines.join('\n')
}

export async function copyText(text: string) {
  await navigator.clipboard.writeText(text)
}
