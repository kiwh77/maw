/**
 * 依赖拓扑布局（从 legacy board-app.js layoutTopoBands 移植）。
 * 波次列 = 完整依赖深度；纵轴并行轨道（barycenter + 单链对齐）。
 */

import type { TopoBand, TopoEdge, TopoTask } from './topology'

const TOPO_NODE_W = 200
const TOPO_NODE_H = 98
const TOPO_GAP_X = 72
const TOPO_GAP_Y = 22
const TOPO_PAD = 20

function topoMedian(values: number[]): number | null {
  if (!values.length) return null
  const s = values.slice().sort((a, b) => a - b)
  const mid = Math.floor((s.length - 1) / 2)
  return s.length % 2 === 1 ? s[mid] : (s[mid] + s[mid + 1]) / 2
}

function topoTrackRound(v: number | null | undefined): number | null {
  if (v == null || Number.isNaN(v)) return null
  return Math.round(v)
}

function topoNearestFreeTrack(want: number, used: Set<number>): number {
  let t = Math.max(0, want | 0)
  if (!used.has(t)) return t
  for (let delta = 1; delta < 10000; delta++) {
    const up = t - delta
    const down = t + delta
    if (up >= 0 && !used.has(up)) return up
    if (!used.has(down)) return down
  }
  return t
}

export function layoutTopoBands(
  tasks: TopoTask[],
  unknownProject = '未知工程',
): TopoBand[] {
  const groups = new Map<string, TopoTask[]>()
  for (const t of tasks) {
    const pid = t.project_id || ''
    if (!groups.has(pid)) groups.set(pid, [])
    groups.get(pid)!.push(t)
  }
  const bands: TopoBand[] = []
  for (const [pid, group] of groups) {
    const byId = new Map(group.map((t) => [t.id, t]))
    const stubs = new Set<string>()
    for (const t of group) {
      for (const d of t.deps || []) if (d && !byId.has(d)) stubs.add(d)
    }
    const level = new Map<string, number>()
    const walk = (id: string, stack: Set<string>): number => {
      if (level.has(id)) return level.get(id)!
      if (stack.has(id)) return 0
      stack.add(id)
      const task = byId.get(id)
      let L = 0
      if (task) {
        for (const d of task.deps || []) {
          if (!d) continue
          L = Math.max(L, (byId.has(d) || stubs.has(d) ? walk(d, stack) : -1) + 1)
        }
      }
      level.set(id, L)
      stack.delete(id)
      return L
    }
    for (const id of stubs) walk(id, new Set())
    for (const t of group) walk(t.id, new Set())
    const layers: string[][] = []
    for (const [id, L] of level) {
      if (!layers[L]) layers[L] = []
      layers[L].push(id)
    }

    const preds = new Map<string, string[]>()
    const succs = new Map<string, string[]>()
    for (const id of level.keys()) {
      preds.set(id, [])
      succs.set(id, [])
    }
    for (const t of group) {
      for (const d of t.deps || []) {
        if (!d || !level.has(d) || !level.has(t.id)) continue
        preds.get(t.id)!.push(d)
        succs.get(d)!.push(t.id)
      }
    }

    const tieBreak = (a: string, b: string) => {
      const ta = byId.get(a)
      const tb = byId.get(b)
      const pa = ta?.priority ?? 1e9
      const pb = tb?.priority ?? 1e9
      if (pa !== pb) return pa - pb
      return a.localeCompare(b)
    }
    for (const layer of layers) {
      if (!layer) continue
      layer.sort(tieBreak)
    }

    for (let pass = 0; pass < 4; pass++) {
      for (let li = 1; li < layers.length; li++) {
        const layer = layers[li]
        const prev = layers[li - 1]
        if (!layer || !prev || !prev.length) continue
        const order = new Map(prev.map((id, i) => [id, i]))
        layer.sort((a, b) => {
          const ma = topoMedian((preds.get(a) || []).map((p) => order.get(p)!).filter((v) => v != null))
          const mb = topoMedian((preds.get(b) || []).map((p) => order.get(p)!).filter((v) => v != null))
          if (ma == null && mb == null) return tieBreak(a, b)
          if (ma == null) return 1
          if (mb == null) return -1
          if (ma !== mb) return ma - mb
          return tieBreak(a, b)
        })
      }
      for (let li = layers.length - 2; li >= 0; li--) {
        const layer = layers[li]
        const next = layers[li + 1]
        if (!layer || !next || !next.length) continue
        const order = new Map(next.map((id, i) => [id, i]))
        layer.sort((a, b) => {
          const ma = topoMedian((succs.get(a) || []).map((s) => order.get(s)!).filter((v) => v != null))
          const mb = topoMedian((succs.get(b) || []).map((s) => order.get(s)!).filter((v) => v != null))
          if (ma == null && mb == null) return tieBreak(a, b)
          if (ma == null) return 1
          if (mb == null) return -1
          if (ma !== mb) return ma - mb
          return tieBreak(a, b)
        })
      }
    }

    const track = new Map<string, number>()
    const desiredOf = (id: string): number | null => {
      const up = (preds.get(id) || []).filter((p) => track.has(p)).map((p) => track.get(p)!)
      const down = (succs.get(id) || []).filter((s) => track.has(s)).map((s) => track.get(s)!)
      if (up.length === 1) return up[0]
      if (up.length >= 2) {
        const m = topoTrackRound(topoMedian(up))
        if (m != null) return m
      }
      if (down.length === 1) return down[0]
      if (down.length >= 2) {
        const m = topoTrackRound(topoMedian(down))
        if (m != null) return m
      }
      return null
    }

    const assignLayer = (layer: string[] | undefined, fallbackStart: number): number => {
      if (!layer || !layer.length) return fallbackStart
      const wants = new Map<string, number>()
      let fb = fallbackStart
      for (const id of layer) {
        let w = desiredOf(id)
        if (w == null) w = fb++
        wants.set(id, w)
      }
      const ordered = layer.slice().sort((a, b) => {
        const d = wants.get(a)! - wants.get(b)!
        if (d !== 0) return d
        return tieBreak(a, b)
      })
      const used = new Set<number>()
      for (const id of ordered) {
        const got = topoNearestFreeTrack(wants.get(id)!, used)
        track.set(id, got)
        used.add(got)
      }
      layer.length = 0
      layer.push(...ordered.sort((a, b) => track.get(a)! - track.get(b)! || tieBreak(a, b)))
      let maxT = fallbackStart
      for (const id of layer) maxT = Math.max(maxT, track.get(id)! + 1)
      return maxT
    }

    let nextFree = 0
    for (let li = 0; li < layers.length; li++) {
      nextFree = Math.max(nextFree, assignLayer(layers[li], nextFree))
    }

    for (let pass = 0; pass < 2; pass++) {
      for (let li = layers.length - 1; li >= 0; li--) {
        const layer = layers[li]
        if (!layer) continue
        for (const id of layer) {
          const outs = (succs.get(id) || []).filter((s) => track.has(s)).map((s) => track.get(s)!)
          const ins = (preds.get(id) || []).filter((p) => track.has(p)).map((p) => track.get(p)!)
          if (outs.length >= 1) {
            const m = outs.length === 1 ? outs[0] : topoTrackRound(topoMedian(outs))
            if (m != null) track.set(id, m)
          } else if (ins.length >= 2) {
            const m = topoTrackRound(topoMedian(ins))
            if (m != null) track.set(id, m)
          }
        }
        const used = new Set<number>()
        const ordered = layer.slice().sort((a, b) => track.get(a)! - track.get(b)! || tieBreak(a, b))
        for (const id of ordered) {
          const got = topoNearestFreeTrack(track.get(id)!, used)
          track.set(id, got)
          used.add(got)
        }
        layer.length = 0
        layer.push(...ordered.sort((a, b) => track.get(a)! - track.get(b)! || tieBreak(a, b)))
      }
      for (let li = 1; li < layers.length; li++) {
        const layer = layers[li]
        if (!layer) continue
        for (const id of layer) {
          const w = desiredOf(id)
          if (w != null) track.set(id, w)
        }
        const used = new Set<number>()
        const ordered = layer.slice().sort((a, b) => track.get(a)! - track.get(b)! || tieBreak(a, b))
        for (const id of ordered) {
          const got = topoNearestFreeTrack(track.get(id)!, used)
          track.set(id, got)
          used.add(got)
        }
        layer.length = 0
        layer.push(...ordered.sort((a, b) => track.get(a)! - track.get(b)! || tieBreak(a, b)))
      }
    }

    const positions = new Map<string, { x: number; y: number; stub?: boolean }>()
    let width = TOPO_PAD * 2
    let height = TOPO_PAD * 2
    layers.forEach((layer, li) => {
      if (!layer) return
      layer.forEach((id) => {
        const x = TOPO_PAD + li * (TOPO_NODE_W + TOPO_GAP_X)
        const y = TOPO_PAD + track.get(id)! * (TOPO_NODE_H + TOPO_GAP_Y)
        positions.set(id, { x, y, stub: !byId.has(id) })
        width = Math.max(width, x + TOPO_NODE_W + TOPO_PAD)
        height = Math.max(height, y + TOPO_NODE_H + TOPO_PAD)
      })
    })
    const edges: TopoEdge[] = []
    for (const t of group) {
      for (const d of t.deps || []) {
        if (!d || !positions.has(d) || !positions.has(t.id)) continue
        edges.push({ from: d, to: t.id, cross: !byId.has(d) })
      }
    }
    bands.push({
      pid,
      name: group[0]?.project_name || pid || unknownProject,
      tasks: group,
      byId,
      positions,
      edges,
      edgeCount: edges.length,
      width,
      height,
    })
  }
  bands.sort((a, b) => a.name.localeCompare(b.name, 'zh'))
  return bands
}
