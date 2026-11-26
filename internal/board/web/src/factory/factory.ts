// @ts-nocheck -- Three.js is used as a runtime renderer; the geometry helpers are tested separately.
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { bedCargo, benchGrid, cargoZone, carryCycle, displayName, factoryGroups, sceneSignature, truckPose, YARD } from './model.mjs';

const instances = new WeakMap();
const colors = { queued: '#c7a575', processing: '#d2aa56', inspection: '#8c9cbb', blocked: '#d07861', stored: '#8ba571', cancelled: '#999e93', unknown: '#9b8da7' };
// A worker follows the most demanding state its owner currently holds.
const zoneRank = { processing: 5, inspection: 4, queued: 3, blocked: 2, unknown: 1, stored: 0, cancelled: 0 };
const zoneRole = { processing: 'carry', inspection: 'inspect', queued: 'dock', blocked: 'blocked', unknown: 'yard', stored: 'rest', cancelled: 'rest' };
const roleStandZ = { dock: YARD.pickupZ, inspect: -3, blocked: 1.5, yard: 0.4, rest: 0.8 };

export function renderFactory(container, options) {
  let view = instances.get(container);
  if (!view) {
    view = createFactory(container);
    instances.set(container, view);
  }
  view.update(options);
}

export function setFactoryVisible(container, visible) {
  instances.get(container)?.setVisible(visible);
}

function createFactory(root) {
  root.innerHTML = '<div class="factory-bar"><p class="factory-note"></p><button type="button" class="factory-home"></button></div><div class="factory-stage"><canvas tabindex="0"></canvas><div class="factory-labels" aria-hidden="true"></div><div class="factory-fallback" hidden></div></div><div class="factory-legend"></div><div class="factory-list"></div>';
  const canvas = root.querySelector('canvas');
  const stage = root.querySelector('.factory-stage');
  const labelsRoot = root.querySelector('.factory-labels');
  const list = root.querySelector('.factory-list');
  const fallback = root.querySelector('.factory-fallback');
  let renderer, controls, scene, camera, world;
  let visible = true, frame = 0, options, signature = '', people = [], trucks = [], pickables = [], labels = [];
  let lastNow = 0;
  const geometries = new Map(), materials = new Map();
  const truckState = new Map(), peopleState = new Map();
  const ray = new THREE.Raycaster();
  const labelPos = new THREE.Vector3();
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  function material(color) {
    if (!materials.has(color)) materials.set(color, new THREE.MeshStandardMaterial({ color, roughness: .85 }));
    return materials.get(color);
  }
  function box(parent, w, h, d, color, x = 0, y = 0, z = 0) {
    const key = `${w},${h},${d}`;
    if (!geometries.has(key)) geometries.set(key, new THREE.BoxGeometry(w, h, d));
    const mesh = new THREE.Mesh(geometries.get(key), material(color));
    mesh.position.set(x, y, z); mesh.castShadow = true; mesh.receiveShadow = true; parent.add(mesh);
    return mesh;
  }
  function group(parent, x, y, z) {
    const g = new THREE.Group(); g.position.set(x, y, z); parent.add(g); return g;
  }
  function addLabel(obj, text) {
    const el = document.createElement('div');
    el.className = 'factory-label';
    // The full Agent id is the nameplate: it is never abbreviated, only positioned.
    el.textContent = text;
    labelsRoot.append(el);
    labels.push({ el, obj, height: 2.45 });
  }
  function clearLabels() {
    labels = [];
    labelsRoot.replaceChildren();
  }
  function resize() {
    if (!renderer || !stage.clientWidth || !stage.clientHeight) return;
    const aspect = stage.clientWidth / stage.clientHeight;
    const half = aspect < 1 ? 17 : 13;
    Object.assign(camera, { left: -half * aspect, right: half * aspect, top: half, bottom: -half });
    camera.updateProjectionMatrix(); renderer.setSize(stage.clientWidth, stage.clientHeight, false);
  }
  function home() {
    if (!camera) return;
    camera.position.set(23, 29, 31); controls.target.set(0, 0, 0); camera.zoom = 1; camera.updateProjectionMatrix();
  }
  root.querySelector('.factory-home').onclick = home;
  try {
    renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
    renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    renderer.shadowMap.enabled = true; renderer.shadowMap.type = THREE.PCFSoftShadowMap;
    renderer.toneMapping = THREE.ACESFilmicToneMapping; renderer.toneMappingExposure = 1.15;
    scene = new THREE.Scene(); camera = new THREE.OrthographicCamera(-20, 20, 13, -13, .1, 150);
    controls = new OrbitControls(camera, canvas); controls.enableDamping = true;
    controls.maxPolarAngle = Math.PI / 2.3; controls.minZoom = .4; controls.maxZoom = 3; controls.enablePan = false;
    scene.add(new THREE.HemisphereLight(0xffffff, 0x6d8265, 2.5));
    const light = new THREE.DirectionalLight(0xfff1d4, 3.4); light.position.set(-12, 25, 16); light.castShadow = true;
    Object.assign(light.shadow.camera, { left: -30, right: 30, top: 30, bottom: -30 });
    light.shadow.mapSize.set(2048, 2048); light.shadow.normalBias = .04; scene.add(light);
    world = new THREE.Group(); scene.add(world); home(); resize();
    new ResizeObserver(resize).observe(stage);
    canvas.addEventListener('webglcontextlost', e => { e.preventDefault(); fallback.hidden = false; fallback.textContent = options?.lang === 'zh' ? '三维画布暂不可用，请使用下方任务列表。' : '3D unavailable. Use the task list below.'; });
    canvas.addEventListener('webglcontextrestored', () => { fallback.hidden = true; signature = ''; if (options) update(options); });
  } catch (_) {
    canvas.hidden = true;
    fallback.hidden = false;
  }
  let pointer;
  canvas.addEventListener('pointerdown', e => pointer = [e.clientX, e.clientY]);
  canvas.addEventListener('pointerup', e => {
    if (!camera || !pointer || Math.hypot(e.clientX - pointer[0], e.clientY - pointer[1]) > 5) return;
    const r = canvas.getBoundingClientRect();
    ray.setFromCamera(new THREE.Vector2((e.clientX-r.left)/r.width*2-1, -(e.clientY-r.top)/r.height*2+1), camera);
    const hit = ray.intersectObjects(pickables, true)[0];
    if (!hit) return;
    let obj = hit.object; while (obj && !obj.userData.task) obj = obj.parent;
    if (obj?.userData.task) options.onTask(obj.userData.task.id, obj.userData.task.project_id || '');
  });
  // A worker: torso, head, cap, eyes, swinging legs, swinging arms and — for the
  // agents that actually carry work — one payload crate held in front.
  function worker(parent, slot, zone, task, name) {
    const role = zoneRole[zone] || 'rest';
    const homeX = slot?.x ?? 0;
    const standZ = role === 'carry' ? (slot?.standZ ?? YARD.standZ)
      : role === 'inspect' ? roleStandZ.inspect
      : role === 'dock' ? roleStandZ.dock
      : role === 'blocked' ? roleStandZ.blocked
      : (slot?.standZ ?? roleStandZ[role] ?? 0.8);
    const g = group(parent, homeX, 0, standZ);
    const tone = role === 'blocked' ? '#ac887c' : role === 'carry' ? '#6d8d7a' : role === 'inspect' ? '#8b92ad' : '#c9a45d';
    box(g, .6, .65, .4, tone, 0, .95, 0);
    box(g, .62, .55, .55, '#dfc2a1', 0, 1.56, 0);
    box(g, .74, .12, .68, tone, 0, 1.87, 0);
    for (const dx of [-.15, .15]) box(g, .065, .075, .025, '#35463d', dx, 1.6, .285);
    const legs = [-.15, .15].map(dx => {
      const leg = group(g, dx, .64, 0);
      box(leg, .22, .55, .25, '#516355', 0, -.2, 0);
      box(leg, .25, .14, .4, '#34483f', 0, -.43, .05);
      return leg;
    });
    const arms = [-.4, .4].map(dx => {
      const a = group(g, dx, 1.2, 0); box(a, .18, .48, .2, tone, 0, -.2, 0); return a;
    });
    const carried = role === 'carry' ? box(g, .42, .4, .4, '#d8c39a', 0, 1.12, .46) : null;
    if (carried) carried.visible = false;
    g.userData.task = task; if (task) pickables.push(g);
    const key = `${task?.project_id || parent.userData.project}/${displayName(name) || 'worker'}`;
    const saved = peopleState.get(key);
    if (saved) { g.position.x = saved.x; g.position.z = saved.z; g.rotation.y = saved.yaw; }
    else g.rotation.y = Math.PI;
    const label = displayName(name || task?.owner || '');
    if (label) addLabel(g, label);
    people.push({
      key, role, g, arms, legs, carried, task,
      phase: saved?.phase ?? Math.random() * Math.PI * 2,
      clock: saved?.clock ?? Math.random() * 3,
      homeX,
      standZ,
      aisleX: slot?.aisleX ?? homeX,
    });
  }
  function makeTruck(lane, length, color, side, projectId) {
    // Built nose-first on local -Z so the pose helper can point it at its travel direction.
    // Deck cargo is a child of the truck group so it translates and yaws with the vehicle.
    const truck = group(lane, 0, 0, 0);
    const cabZ = -length / 2, bedZ = YARD.bedZ;
    box(truck, 2.7, .28, length + 1, '#576c59', 0, .55, 0);
    box(truck, 2.6, 1.6, 1.3, color, 0, 1.5, cabZ);
    box(truck, 2.25, .65, .05, '#bad0c9', 0, 1.8, cabZ - .68);
    box(truck, 2.5, .25, .14, '#e3e7d8', 0, .95, cabZ - .72);
    for (const dx of [-.9, .9]) box(truck, .4, .22, .08, '#fff0c4', dx, 1.2, cabZ - .7);
    box(truck, 2.6, .15, length - .3, '#baa98a', 0, .8, bedZ);
    for (const dx of [YARD.bedWidth / -2, YARD.bedWidth / 2]) {
      box(truck, .1, .7, length - .3, color, dx, 1.2, bedZ);
      for (const dz of [cabZ + .35, length / 2 - .15]) box(truck, .25, .65, .7, '#374a40', dx, .37, dz);
    }
    const deck = group(truck, 0, 0, 0);
    const saved = truckState.get(projectId);
    trucks.push({ g: truck, deck, side, length, projectId, arrive: saved ?? (reduced.matches ? 1 : 0) });
    return trucks[trucks.length - 1];
  }
  function crate(parent, task, zone, x, y, z) {
    const g = group(parent, x, y, z);
    box(g, YARD.crates.size, .52, YARD.crates.size, colors[zone], 0, .26, 0);
    box(g, .07, .015, YARD.crates.size + .01, '#e9d9b5', 0, .528, 0);
    g.userData.task = task; pickables.push(g);
    return g;
  }
  function addWorkbench(lane, x, z) {
    box(lane, 1.15, .18, 1.15, '#c5b38b', x, 1.28, z);
    for (const dx of [-.42, .42]) {
      for (const dz of [-.42, .42]) box(lane, .1, 1.05, .1, '#75896c', x + dx, .55, z + dz);
    }
    box(lane, 1.05, .04, .08, '#a8946a', x, 1.4, z - .5);
  }
  function rebuild(groups, agents) {
    // Remember where every moving piece was, so a new snapshot never restarts a
    // walk-in or a carry cycle. Polling only re-places static geometry.
    for (const p of people) peopleState.set(p.key, { x: p.g.position.x, z: p.g.position.z, yaw: p.g.rotation.y, clock: p.clock, phase: p.phase });
    for (const t of trucks) truckState.set(t.projectId, t.arrive);
    world.clear(); people = []; trucks = []; pickables = []; clearLabels();
    const shown = groups.slice(0, 6);
    // Yard figures follow live task ownership (processing / review / blocked…), not the
    // agents-rail heartbeat. Idle stand-ins with no active cargo stay off the pad.
    // Pre-size lanes for the densest *active* agent grid so benches stay inside the pad.
    let lanePitch = YARD.laneWidth;
    for (const g of shown) {
      const owners = new Set();
      for (const task of g.tasks.slice(0, 72)) {
        if (task.owner && !['stored', 'cancelled', 'queued'].includes(cargoZone(task.status))) {
          owners.add(task.owner);
        }
      }
      const grid = benchGrid(owners.size);
      lanePitch = Math.max(lanePitch, grid.width + 3.6);
    }
    const width = Math.max(16, shown.length * lanePitch);
    box(world, width + 2, .45, 26, '#cbd7bf', 0, -.3, 0);
    box(world, width + 1, .1, 25, '#e0e6d5', 0, -.04, 0);
    box(world, width, .03, 5, '#a5b49b', 0, .04, YARD.roadZ);
    shown.forEach((g, index) => {
      const x = (index - (shown.length - 1) / 2) * lanePitch;
      const lane = group(world, x, 0, 0);
      lane.userData.project = g.id;
      const large = g.size !== 'small', length = large ? 4.8 : 3.1;
      const color = large ? '#6b8b78' : '#c4a45c';
      const truck = makeTruck(lane, length, color, index % 2 ? 1 : -1, g.id);
      // Unloading dock, shared inspection, warehouse. Workbenches in a grid below.
      box(lane, Math.max(5.6, lanePitch - 1), .35, YARD.dockDepth, '#c0cbb4', 0, .15, YARD.dockZ);
      box(lane, 2.8, .3, 1.6, '#bac6d1', 0, .4, -4);
      for (const bx of [-2.5, 2.5]) box(lane, .12, 3.8, 1.7, '#6d8465', bx, 1.9, -7);
      for (const y of [.2, 1.4, 2.6, 3.8]) box(lane, 5.2, .12, 1.8, '#a9bc99', 0, y, -7);
      const counts = {}, owners = new Map(), procCounts = {};
      const sample = g.tasks.slice(0, 72);
      for (const task of sample) {
        const zone = cargoZone(task.status);
        // Skip owners who only hold queued/done cargo — those are not "executing".
        if (task.owner && !['stored', 'cancelled', 'queued'].includes(zone)) {
          const held = owners.get(task.owner);
          if (!held) owners.set(task.owner, { zone, task });
          else if ((zoneRank[zone] ?? 0) > (zoneRank[held.zone] ?? 0)) { held.zone = zone; held.task = task; }
        }
      }
      // Only agents currently executing work appear in the yard (no idle stand-ins).
      const active = [...owners];
      const roster = active.map(([owner]) => owner);
      const grid = benchGrid(roster.length);
      const rosterIndex = new Map(roster.map((id, i) => [id, i]));
      grid.slots.forEach((slot) => addWorkbench(lane, slot.x, slot.z));
      active.forEach(([owner, o]) => {
        const i = rosterIndex.get(owner) ?? 0;
        worker(lane, grid.slots[i], o.zone, { ...o.task, project_id: g.id }, owner);
      });
      for (const task of sample) {
        const zone = cargoZone(task.status), n = counts[zone] || 0; counts[zone] = n + 1;
        if (zone === 'queued') {
          // Deck-local child of the truck → same translation/yaw as the vehicle.
          const slot = bedCargo(n, length);
          crate(truck.deck, task, zone, slot.x, slot.y, slot.z);
        } else if (zone === 'stored') {
          const row = Math.floor(n / 5);
          crate(lane, task, zone, (n % 5 - 2) * .8, .3 + (row % 3) * 1.2, -7 + (Math.floor(row / 3) % 2) * .7);
        } else if (zone === 'processing') {
          const ai = rosterIndex.has(task.owner) ? rosterIndex.get(task.owner) : 0;
          const key = 'proc-' + ai;
          const pn = procCounts[key] || 0; procCounts[key] = pn + 1;
          const slot = grid.slots[ai] || { x: 0, z: YARD.benchZ };
          crate(lane, task, zone, slot.x + (pn % 2 ? .28 : -.28), 1.42 + Math.floor(pn / 2) * .5, slot.z);
        } else if (zone === 'inspection') {
          crate(lane, task, zone, (n % 4 - 1.5) * .65, .58 + Math.floor(n / 4) * .55, -4);
        } else {
          crate(lane, task, zone, -2.2 + (n % 3) * .7, .1 + Math.floor(n / 6) * .55, 1.6 + Math.floor((n % 6) / 3) * .7);
        }
      }
    });
    for (const key of [...peopleState.keys()]) if (!people.some(p => p.key === key)) peopleState.delete(key);
    for (const id of [...truckState.keys()]) if (!trucks.some(t => t.projectId === id)) truckState.delete(id);
    const extent = Math.max(1, width / 28); camera.zoom = 1 / extent; camera.updateProjectionMatrix();
    placeTrucks();
  }
  function applyTheme() {
    const dark = getComputedStyle(root).colorScheme === 'dark';
    if (scene) scene.background = new THREE.Color(dark ? '#28352e' : '#e9eee4');
  }
  new MutationObserver(applyTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);
  function update(next) {
    options = next;
    const zh = next.lang === 'zh';
    const groups = factoryGroups(next.tasks);
    const words = zh ? { queued: '待卸货', processing: '处理中', inspection: '待验收 / 合入', blocked: '阻塞 / 冲突', stored: '已入库', cancelled: '已取消', unknown: '未知状态' } : { queued: 'Queued', processing: 'Processing', inspection: 'Inspection / merge', blocked: 'Blocked / conflict', stored: 'Stored', cancelled: 'Cancelled', unknown: 'Unknown' };
    root.querySelector('.factory-note').textContent = zh ? '实时任务 · 每工程一车，每任务一件货。入库仅对应 done；当前筛选不代表整个工程已完成。' : 'Live tasks · One truck per project, one box per task. Stored = done. Filtered results do not imply project completion.';
    root.querySelector('.factory-home').textContent = zh ? '回到全景' : 'Reset camera';
    canvas.setAttribute('aria-label', zh ? '工坊三维场景；下方提供可键盘操作的全部任务' : 'Workshop 3D scene. All tasks are available in the keyboard accessible list below.');
    if (!renderer) fallback.textContent = zh ? '当前环境不支持 WebGL，请使用下方实时任务列表。' : 'WebGL unavailable. Use the live task list below.';
    root.querySelector('.factory-legend').replaceChildren(...Object.entries(words).map(([key, label]) => { const el = document.createElement('span'); const dot = document.createElement('i'); dot.style.background = colors[key]; el.append(dot, document.createTextNode(label)); return el; }));
    const sig = sceneSignature(next.tasks, next.agents || [], next.lang);
    if (sig !== signature) {
      signature = sig;
      if (renderer) rebuild(groups, next.agents || []);
      list.replaceChildren();
      if (!groups.length) { const p = document.createElement('p'); p.className = 'empty'; p.textContent = zh ? '当前筛选下没有任务' : 'No tasks match the current filters'; list.append(p); }
      if (groups.length > 6 || groups.some(g => g.tasks.length > 72)) { const p = document.createElement('p'); p.className = 'factory-note'; p.textContent = zh ? '三维画布最多展示前 6 个工程、每工程前 72 件货物。完整结果见下方，或按工程筛选。' : '3D shows the first 6 projects and 72 boxes per project. All matching tasks are listed below; filter by project for detail.'; list.append(p); }
      for (const g of groups) {
        const section = document.createElement('details'); section.open = groups.length === 1;
        const summary = document.createElement('summary'); summary.textContent = `${g.name} · ${g.tasks.length} ${zh ? '件货物' : 'tasks'} · ${g.counts.stored || 0} ${zh ? '已入库' : 'stored'}`; section.append(summary);
        for (const t of g.tasks) { const b = document.createElement('button'); b.type = 'button'; b.className = 'factory-task'; b.textContent = `${t.id} · ${t.title} · ${words[cargoZone(t.status)]}${t.owner ? ' · ' + t.owner : ''}`; b.onclick = () => options.onTask(t.id, t.project_id || ''); section.append(b); }
        list.append(section);
      }
    }
    applyTheme();
  }
  function placeTrucks() {
    for (const t of trucks) {
      const pose = truckPose(reduced.matches ? 1 : t.arrive, t.side, t.length);
      t.g.position.set(pose.x, 0, pose.z);
      t.g.rotation.y = pose.yaw;
    }
  }
  function posePerson(p, walking, carrying, working, t) {
    p.legs.forEach((leg, i) => {
      leg.rotation.x = walking ? Math.sin(t * 9 + i * Math.PI + p.phase) * .5 : 0;
    });
    p.arms.forEach((a, i) => {
      if (p.role === 'blocked') a.rotation.x = -2.5;
      else if (carrying) a.rotation.x = -1.15;
      else if (walking) a.rotation.x = Math.sin(t * 9 + i * Math.PI + p.phase) * .42;
      else if (working) a.rotation.x = -.78 + (t ? Math.sin(t * 3.2 + i) * .28 : 0);
      else a.rotation.x = t ? Math.sin(t * 1.3 + i + p.phase) * .1 : 0;
    });
    if (p.carried) p.carried.visible = carrying;
  }
  function animatePeople(now, dt) {
    const t = now * .001;
    for (const p of people) {
      if (reduced.matches) {
        // Reduced motion: every worker freezes at its station, cargo stays on the vehicle.
        p.g.position.set(p.homeX, 0, p.standZ); p.g.rotation.y = Math.PI;
        posePerson(p, false, false, p.role === 'carry' || p.role === 'inspect', 0);
        continue;
      }
      if (p.role === 'carry') {
        // Shuttle via side aisle: never cut through a desk top.
        p.clock += dt;
        const step = carryCycle(p.clock, p.standZ, p.homeX, p.aisleX);
        p.g.position.x = step.x;
        p.g.position.z = step.z;
        // Face the route while walking; lock yaw at the bench so only arms move.
        p.g.rotation.y = step.walking ? step.facing : Math.PI;
        posePerson(p, step.walking, step.carrying, step.working, t);
        continue;
      }
      const dz = p.standZ - p.g.position.z, dx = p.homeX - p.g.position.x;
      const dist = Math.hypot(dx, dz);
      let walking = false;
      if (dist > .05) {
        const step = Math.min(dist, 1.7 * dt);
        p.g.position.x += dx / dist * step;
        p.g.position.z += dz / dist * step;
        p.g.rotation.y = Math.atan2(dx, dz);
        walking = true;
      } else {
        // Planted at the station: body faces the bench; only arms animate.
        p.g.position.x = p.homeX;
        p.g.position.z = p.standZ;
        p.g.rotation.y = Math.PI;
      }
      posePerson(p, walking, false, !walking && p.role !== 'rest', t);
    }
  }
  function projectLabels() {
    if (!camera) return;
    const w = stage.clientWidth, h = stage.clientHeight;
    for (const item of labels) {
      item.obj.getWorldPosition(labelPos);
      labelPos.y += item.height;
      labelPos.project(camera);
      const onScreen = Math.abs(labelPos.x) <= 1.05 && Math.abs(labelPos.y) <= 1.05 && labelPos.z <= 1;
      item.el.style.display = onScreen ? '' : 'none';
      if (!onScreen) continue;
      item.el.style.left = `${(labelPos.x * .5 + .5) * w}px`;
      item.el.style.top = `${(-labelPos.y * .5 + .5) * h}px`;
    }
  }
  function animate(now) {
    frame = 0; if (!visible || !renderer || !root.isConnected) return;
    const dt = lastNow ? Math.min(.05, (now - lastNow) / 1000) : .016;
    lastNow = now;
    if (!document.hidden) {
      if (!reduced.matches) {
        for (const t of trucks) {
          if (t.arrive < 1) { t.arrive = Math.min(1, t.arrive + dt * .4); truckState.set(t.projectId, t.arrive); }
        }
      }
      placeTrucks();
      animatePeople(now, dt);
      // Nameplates track the current transforms, so a snapshot that re-places the
      // yard never leaves labels behind for a frame.
      world.updateMatrixWorld(true);
      projectLabels();
      controls.update(); renderer.render(scene, camera);
    }
    frame = requestAnimationFrame(animate);
  }
  function setVisible(value) { visible = value; if (!value && frame) { cancelAnimationFrame(frame); frame = 0; lastNow = 0; } if (value) { resize(); if (!frame) frame = requestAnimationFrame(animate); } }
  setVisible(true);
  return { update, setVisible };
}
