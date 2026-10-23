// @ts-nocheck -- Three.js is used as a runtime renderer; data mapping is tested separately.
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { cargoZone, factoryGroups } from './model.mjs';

const instances = new WeakMap();
const colors = { queued: '#c7a575', processing: '#d2aa56', inspection: '#8c9cbb', blocked: '#d07861', stored: '#8ba571', cancelled: '#999e93', unknown: '#9b8da7' };

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
  root.innerHTML = '<div class="factory-bar"><p class="factory-note"></p><button type="button" class="factory-home"></button></div><div class="factory-stage"><canvas tabindex="0"></canvas><div class="factory-fallback" hidden></div></div><div class="factory-legend"></div><div class="factory-list"></div>';
  const canvas = root.querySelector('canvas');
  const stage = root.querySelector('.factory-stage');
  const list = root.querySelector('.factory-list');
  const fallback = root.querySelector('.factory-fallback');
  let renderer, controls, scene, camera, world;
  let visible = true, frame = 0, options, signature = '', people = [], pickables = [];
  const geometries = new Map(), materials = new Map();
  const ray = new THREE.Raycaster();
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
  function worker(parent, x, z, color, task) {
    const g = group(parent, x, 0, z);
    box(g, .6, .65, .4, color, 0, .95, 0);
    box(g, .62, .55, .55, '#dfc2a1', 0, 1.56, 0);
    box(g, .74, .12, .68, color, 0, 1.87, 0);
    for (const dx of [-.15,.15]) {
      box(g, .065,.075,.025,'#35463d',dx,1.6,.285);
      box(g,.22,.55,.25,'#516355',dx,.4,0);
      box(g,.25,.14,.4,'#34483f',dx,.12,.05);
    }
    const arms = [-.4,.4].map(dx => {
      const a = group(g, dx, 1.2, 0); box(a,.18,.48,.2,color,0,-.2,0); return a;
    });
    g.userData.task = task; if (task) pickables.push(g);
    people.push({ g, arms, active: task && ['processing','inspection'].includes(cargoZone(task.status)), blocked: task && cargoZone(task.status) === 'blocked' });
  }
  function rebuild(groups, agents) {
    world.clear(); people = []; pickables = [];
    const shown = groups.slice(0,6), width = Math.max(16, shown.length * 7);
    box(world,width+2,.45,21,'#cbd7bf',0,-.3,0);
    box(world,width+1,.1,20,'#e0e6d5',0,-.04,0);
    box(world,width,.03,5,'#a5b49b',0,.04,7.3);
    const workerNames = new Set();
    shown.forEach((g, index) => {
      const x = (index - (shown.length-1)/2)*7;
      const lane = group(world,x,0,0);
      const large = g.size !== 'small', length = large ? 4.8 : 3.1;
      const truck = group(lane,0,0,6.5);
      const color = large ? '#6b8b78' : '#c4a45c';
      box(truck,2.7,.28,length+1,'#576c59',0,.55,0);
      box(truck,2.6,1.6,1.3,color,0,1.5,length/2);
      box(truck,2.25,.65,.05,'#bad0c9',0,1.8,length/2+.68);
      box(truck,2.6,.15,length-.3,'#baa98a',0,.8,-.7);
      for(const dx of [-1.3,1.3]) {
        box(truck,.1,.7,length-.3,color,dx,1.2,-.7);
        for(const dz of [-length/2+.4,length/2]) box(truck,.25,.65,.7,'#374a40',dx,.37,dz);
      }
      // Delivery bay, processing bench, inspection station, warehouse.
      box(lane,5.6,.35,3,'#c0cbb4',0,.15,2.4);
      for(const bx of [-1.1,1.1]) box(lane,.13,1.2,1.1,'#75896c',bx,.6,-1.5);
      box(lane,3,.2,1.5,'#c5b38b',0,1.3,-1.5);
      box(lane,2.8,.3,1.6,'#bac6d1',0,.4,-4);
      for(const bx of [-2.5,2.5]) box(lane,.12,3.8,1.7,'#6d8465',bx,1.9,-7);
      for(const y of [.2,1.4,2.6,3.8]) box(lane,5.2,.12,1.8,'#a9bc99',0,y,-7);
      const counts = {};
      const sample = g.tasks.slice(0,72);
      for(const task of sample) {
        const zone = cargoZone(task.status), n = counts[zone] || 0; counts[zone]=n+1;
        const crate = group(lane,0,0,0);
        const col = n%5, row = Math.floor(n/5);
        if(zone==='queued') crate.position.set((n%3-1)*.7,1+Math.floor(n/12)*.55,6.4-Math.floor((n%12)/3)*.6);
        else if(zone==='stored') crate.position.set((col-2)*.8,.3+(row%3)*1.2,-7+(Math.floor(row/3)%2)*.7);
        else if(zone==='processing') crate.position.set((n%4-1.5)*.65,1.45+Math.floor(n/4)*.55,-1.5);
        else if(zone==='inspection') crate.position.set((n%4-1.5)*.65,.58+Math.floor(n/4)*.55,-4);
        else crate.position.set(-2.2+(n%3)*.7,.1+Math.floor(n/6)*.55,1.6+Math.floor((n%6)/3)*.7);
        box(crate,.55,.52,.55,colors[zone],0,.26,0);box(crate,.07,.015,.56,'#e9d9b5',0,.528,0);
        crate.userData.task=task;pickables.push(crate);
        if(task.owner && !workerNames.has(`${g.id}/${task.owner}`) && !['stored','cancelled'].includes(zone)) {
          workerNames.add(`${g.id}/${task.owner}`);
          const wi=[...workerNames].filter(name=>name.startsWith(`${g.id}/`)).length-1;
          if(wi<4) worker(lane,-2+wi*1.3,-.2,['#6d8d7a','#c9a45d','#8b92ad','#ac887c'][wi],task);
        }
      }
      // Idle registered agents remain visible, without a fictional assignment.
      const idle = agents.filter(a=>a.project_id===g.id&&!a.task_id&&a.status!=='offline').slice(0,2);
      idle.forEach((a,i)=>worker(lane,2+i*.8,3,'#99a58d',null));
    });
    const extent = Math.max(1,width/28); camera.zoom=1/extent;camera.updateProjectionMatrix();
  }
  function applyTheme() {
    const dark = getComputedStyle(root).colorScheme === 'dark';
    if (scene) scene.background = new THREE.Color(dark ? '#28352e' : '#e9eee4');
  }
  new MutationObserver(applyTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);
  function update(next) {
    options=next;
    const zh=next.lang==='zh';
    const groups=factoryGroups(next.tasks);
    const words=zh?{queued:'待卸货',processing:'处理中',inspection:'待验收 / 合入',blocked:'阻塞 / 冲突',stored:'已入库',cancelled:'已取消',unknown:'未知状态'}:{queued:'Queued',processing:'Processing',inspection:'Inspection / merge',blocked:'Blocked / conflict',stored:'Stored',cancelled:'Cancelled',unknown:'Unknown'};
    root.querySelector('.factory-note').textContent=zh?'实时任务 · 每工程一车，每任务一件货。入库仅对应 done；当前筛选不代表整个工程已完成。':'Live tasks · One truck per project, one box per task. Stored = done. Filtered results do not imply project completion.';
    root.querySelector('.factory-home').textContent=zh?'回到全景':'Reset camera';
    canvas.setAttribute('aria-label',zh?'收货工坊三维场景；下方提供可键盘操作的全部任务':'3D receiving yard. All tasks are available in the keyboard accessible list below.');
    if(!renderer) fallback.textContent=zh?'当前环境不支持 WebGL，请使用下方实时任务列表。':'WebGL unavailable. Use the live task list below.';
    root.querySelector('.factory-legend').replaceChildren(...Object.entries(words).map(([key,label])=>{const el=document.createElement('span');const dot=document.createElement('i');dot.style.background=colors[key];el.append(dot,document.createTextNode(label));return el;}));
    const sig=JSON.stringify([next.tasks.map(t=>[t.project_id,t.id,t.status,t.owner,t.title]),next.agents?.map(a=>[a.project_id,a.id,a.status,a.task_id]),zh]);
    if(sig!==signature) {
      signature=sig;
      if(renderer)rebuild(groups,next.agents||[]);
      list.replaceChildren();
      if(!groups.length){const p=document.createElement('p');p.className='empty';p.textContent=zh?'当前筛选下没有任务':'No tasks match the current filters';list.append(p);}
      if(groups.length>6||groups.some(g=>g.tasks.length>72)){const p=document.createElement('p');p.className='factory-note';p.textContent=zh?'三维画布最多展示前 6 个工程、每工程前 72 件货物。完整结果见下方，或按工程筛选。':'3D shows the first 6 projects and 72 boxes per project. All matching tasks are listed below; filter by project for detail.';list.append(p);}
      for(const g of groups) {
        const section=document.createElement('details');section.open=groups.length===1;
        const summary=document.createElement('summary');summary.textContent=`${g.name} · ${g.tasks.length} ${zh?'件货物':'tasks'} · ${g.counts.stored||0} ${zh?'已入库':'stored'}`;section.append(summary);
        for(const t of g.tasks){const b=document.createElement('button');b.type='button';b.className='factory-task';b.textContent=`${t.id} · ${t.title} · ${words[cargoZone(t.status)]}${t.owner?' · '+t.owner:''}`;b.onclick=()=>options.onTask(t.id,t.project_id||'');section.append(b);}
        list.append(section);
      }
    }
    applyTheme();
  }
  function animate(now) {
    frame=0;if(!visible||!renderer||!root.isConnected)return;
    if(!document.hidden){
      for(const p of people)p.arms.forEach((a,i)=>a.rotation.x=p.blocked?-2.5:p.active?-.7+(reduced.matches?0:Math.sin(now*.004+i)*.09):0);
      controls.update();renderer.render(scene,camera);
    }
    frame=requestAnimationFrame(animate);
  }
  function setVisible(value){visible=value;if(!value&&frame){cancelAnimationFrame(frame);frame=0;}if(value){resize();if(!frame)frame=requestAnimationFrame(animate);}}
  setVisible(true);
  return {update,setVisible};
}
