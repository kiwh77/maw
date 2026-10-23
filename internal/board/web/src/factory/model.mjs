// Only authoritative task states determine cargo placement. Animation never completes work.
export function cargoZone(status) {
  if (status === 'done') return 'stored';
  if (status === 'cancelled') return 'cancelled';
  if (status === 'blocked' || status === 'conflict') return 'blocked';
  if (['claimed', 'in_progress', 'self_check'].includes(status)) return 'processing';
  if (['in_review', 'merging'].includes(status)) return 'inspection';
  if (['ready', 'backlog'].includes(status)) return 'queued';
  return 'unknown';
}
export function factoryGroups(tasks) {
  const groups = new Map();
  for (const task of tasks) {
    const id = task.project_id || '';
    if (!groups.has(id)) groups.set(id, { id, name: task.project_name || id || '—', tasks: [], counts: {} });
    const g = groups.get(id), zone = cargoZone(task.status);
    g.tasks.push(task); g.counts[zone] = (g.counts[zone] || 0) + 1;
  }
  for (const g of groups.values()) {
    g.complete = g.tasks.length > 0 && g.counts.stored === g.tasks.length;
    g.size = g.tasks.length <= 5 ? 'small' : g.tasks.length <= 20 ? 'medium' : 'large';
  }
  return [...groups.values()];
}

// The nameplate has to identify the Agent, so the whole owner string is rendered.
// Only surrounding/inner whitespace is normalised; nothing is shortened.
export function displayName(value) {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

// Lane-local yard geometry. z grows toward the road and shrinks toward the warehouse,
// so -Z is "into the shed" and +Z is "out on the road".
export const YARD = {
  laneWidth: 7,
  dockZ: 2.4,        // centre of the unloading dock slab
  dockDepth: 3,
  dockFront: 3.9,    // road-facing edge of the dock = where the tail has to end up
  tailZ: 3.3,        // parked tail position, kept just inside the dock edge
  benchZ: -1.5,      // processing bench centre
  standZ: -0.6,      // where a worker stands to serve the bench
  pickupZ: 3.25,     // where a worker takes a crate off the open tail
  roadZ: 9.4,        // centre of the road the trucks drive along
  roadX: 12,         // spawn distance along the road, just outside the yard pad
  bedZ: 0.7,         // deck centre in truck space (cab sits on local -Z)
  bedWidth: 2.6,
  crates: { size: 0.55, pitch: 0.7, layer: 0.55 },
};

// Reversal is a separate leg: trucks drive in nose-first, then back the tail onto the dock.
export const TRUCK = { approach: 0.55, speed: 0.4 };

function easeOut(u) {
  const p = Math.min(1, Math.max(0, u));
  return 1 - Math.pow(1 - p, 3);
}

function normalizeAngle(value) {
  let a = value;
  while (a > Math.PI) a -= Math.PI * 2;
  while (a < -Math.PI) a += Math.PI * 2;
  return a;
}

// Rearmost deck point in truck-local space (local -Z is the cab, +Z is the open tail).
export function truckTailLocal(length) {
  return YARD.bedZ + (length - 0.3) / 2;
}

// Parked with the tail over the dock: the truck is squared up so the cab faces the road.
export function truckTailZ(length) {
  return YARD.tailZ;
}

export function truckBayZ(length) {
  return YARD.tailZ + truckTailLocal(length);
}

// Pose of a truck at animation progress `progress` (0 = off-lane, 1 = docked).
// Leg 1 drives along the road with the nose leading; leg 2 reverses onto the dock.
export function truckPose(progress, side, length) {
  const dir = side >= 0 ? 1 : -1;
  const approachYaw = dir > 0 ? Math.PI / 2 : -Math.PI / 2;
  const finalYaw = Math.PI;
  const bayZ = truckBayZ(length);
  const p = Math.min(1, Math.max(0, Number.isFinite(progress) ? progress : 0));
  if (p < TRUCK.approach) {
    const u = easeOut(p / TRUCK.approach);
    return { x: dir * YARD.roadX * (1 - u), z: YARD.roadZ, yaw: approachYaw, reversing: false, docked: false };
  }
  const u = easeOut((p - TRUCK.approach) / (1 - TRUCK.approach));
  const delta = normalizeAngle(finalYaw - approachYaw);
  return {
    x: 0,
    z: YARD.roadZ + (bayZ - YARD.roadZ) * u,
    yaw: approachYaw + delta * u,
    reversing: true,
    docked: u >= 1,
  };
}

// Unit heading of the truck nose for a yaw (model nose points at local -Z).
export function truckHeading(yaw) {
  return { x: -Math.sin(yaw), z: -Math.cos(yaw) };
}

// Cargo stacked on the deck, in truck-local space so it travels with the vehicle.
export function bedCargo(index, length) {
  const deck = length - 0.3;
  const { pitch, layer } = YARD.crates;
  const rows = Math.max(1, Math.floor((deck - 0.9) / pitch) + 1);
  const perLayer = rows * 3;
  const stack = Math.floor(index / perLayer);
  const rest = index % perLayer;
  return {
    x: ((rest % 3) - 1) * pitch,
    y: 1 + stack * layer,
    z: YARD.bedZ + (deck / 2 - 0.45) - Math.floor(rest / 3) * pitch,
  };
}

// Worker shuttle: empty walk to the tail, carry to the bench, work, walk back.
export const CARRY = { speed: 1.6, pickup: 0.5, work: 1.6 };

export function carryWalkTime(standZ = YARD.standZ) {
  return Math.max(0.8, Math.abs(YARD.pickupZ - standZ) / CARRY.speed);
}

export function carryPeriod(standZ = YARD.standZ) {
  return CARRY.pickup + carryWalkTime(standZ) * 2 + CARRY.work;
}

// Pure function of elapsed seconds, so polling can never restart a cycle and the
// animation can never touch task state. `standZ` is that agent's own bench front.
export function carryCycle(elapsed, standZ = YARD.standZ) {
  const to = Number.isFinite(standZ) ? standZ : YARD.standZ;
  const walk = carryWalkTime(to);
  const total = carryPeriod(to);
  const t0 = Number.isFinite(elapsed) ? elapsed : 0;
  let t = ((t0 % total) + total) % total;
  const from = YARD.pickupZ;
  if (t < CARRY.pickup) return { z: from, walking: false, carrying: false, working: false, facing: Math.PI };
  t -= CARRY.pickup;
  if (t < walk) {
    const u = t / walk;
    return { z: from + (to - from) * u, walking: true, carrying: true, working: false, facing: Math.PI };
  }
  t -= walk;
  if (t < CARRY.work) return { z: to, walking: false, carrying: false, working: true, facing: Math.PI };
  const u = (t - CARRY.work) / walk;
  return { z: to + (from - to) * u, walking: true, carrying: false, working: false, facing: 0 };
}

/** Pack workbenches into a grid in the lane (cols × rows), growing toward the warehouse. */
export function benchGrid(count, opts = {}) {
  const n = Math.max(0, Number(count) || 0);
  if (n === 0) return { cols: 0, rows: 0, slots: [], width: 0, depth: 0 };
  const maxCols = opts.maxCols ?? 4;
  const pitchX = opts.pitchX ?? 1.4;
  const pitchZ = opts.pitchZ ?? 1.5;
  const standLift = YARD.standZ - YARD.benchZ;
  const cols = Math.min(maxCols, Math.max(1, Math.ceil(Math.sqrt(n))));
  const rows = Math.ceil(n / cols);
  const width = (cols - 1) * pitchX;
  const depth = (rows - 1) * pitchZ;
  const slots = [];
  for (let i = 0; i < n; i++) {
    const col = i % cols;
    const row = Math.floor(i / cols);
    const x = cols === 1 ? 0 : -width / 2 + col * pitchX;
    const z = YARD.benchZ - row * pitchZ;
    slots.push({ x, z, standZ: z + standLift, col, row });
  }
  return { cols, rows, slots, width, depth };
}

const signatureKey = row => JSON.stringify(row);

// Stable key for "the rendered scene would change": order-insensitive, so a poll that
// only reshuffles the payload never rebuilds the yard and never replays the animation.
export function sceneSignature(tasks, agents, lang) {
  const taskRows = (tasks || [])
    .map(t => [t.project_id || '', t.id || '', t.status || '', t.owner || '', t.title || ''])
    .map(signatureKey)
    .sort();
  const agentRows = (agents || [])
    .map(a => [a.project_id || '', a.id || '', a.status || '', a.task_id || ''])
    .map(signatureKey)
    .sort();
  return JSON.stringify([taskRows, agentRows, lang === 'zh' ? 'zh' : 'en']);
}
