const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const load = () => import('./model.mjs');

test('nameplates keep the whole Agent id instead of an abbreviation', async () => {
  const { displayName } = await load();
  assert.equal(displayName('codebuddy-goalagents-9'), 'codebuddy-goalagents-9');
  assert.equal(displayName('  workbuddy-goalagents-12\n'), 'workbuddy-goalagents-12');
  assert.equal(displayName('trae  goalagents  4'), 'trae goalagents 4');
  assert.equal(displayName(undefined), '');
  assert.ok(!displayName('cursor-goalagents-2').includes('…'));
});

test('trucks drive in nose-first and reverse the tail onto the dock', async () => {
  const { YARD, truckBayZ, truckHeading, truckPose, truckTailLocal, TRUCK } = await load();
  for (const side of [1, -1]) {
    for (const length of [3.1, 4.8]) {
      let previous = truckPose(0, side, length);
      assert.equal(previous.docked, false);
      // Leg 1: the vehicle travels in the direction its nose points.
      for (let p = 0.02; p < TRUCK.approach - 0.02; p += 0.02) {
        const pose = truckPose(p, side, length);
        const nose = truckHeading(pose.yaw);
        const dot = nose.x * (pose.x - previous.x) + nose.z * (pose.z - previous.z);
        assert.ok(dot > 0, `side=${side} p=${p} must approach nose-first`);
        assert.equal(pose.z, YARD.roadZ);
        previous = pose;
      }
      // Leg 2: the truck backs up, so the tail reaches the dock first.
      for (let p = TRUCK.approach + 0.03; p <= 1; p += 0.03) {
        const pose = truckPose(p, side, length);
        const nose = truckHeading(pose.yaw);
        const dot = nose.x * (pose.x - previous.x) + nose.z * (pose.z - previous.z);
        assert.ok(dot < 0, `side=${side} p=${p} must reverse into the bay`);
        previous = pose;
      }
      const parked = truckPose(1, side, length);
      assert.equal(parked.docked, true);
      assert.equal(parked.x, 0);
      assert.equal(Math.abs(parked.yaw), Math.PI);
      // Parked square to the dock, cab toward the road, tail over the dock slab.
      const tailZ = truckBayZ(length) - truckTailLocal(length);
      assert.ok(Math.abs(tailZ - YARD.tailZ) < 1e-9);
      assert.ok(tailZ < YARD.dockFront, 'tail must stay inside the dock edge');
      assert.ok(tailZ > YARD.dockZ - YARD.dockDepth / 2, 'tail must reach the dock slab');
      const cabZ = truckBayZ(length) + length / 2;
      assert.ok(cabZ > tailZ && cabZ > YARD.dockFront, 'cab has to face the road, away from the dock');
    }
  }
});

test('waiting cargo is stacked on the deck and rides with the truck', async () => {
  const { bedCargo, truckBayZ, truckTailLocal, truckPose, YARD } = await load();
  const toWorld = (slot, pose) => {
    const c = Math.cos(pose.yaw), s = Math.sin(pose.yaw);
    return { x: pose.x + slot.x * c + slot.z * s, z: pose.z - slot.x * s + slot.z * c };
  };
  for (const length of [3.1, 4.8]) {
    const deck = length - 0.3;
    for (let n = 0; n < 18; n += 1) {
      const slot = bedCargo(n, length);
      // Truck-local offsets stay on the deck, in every direction.
      assert.ok(Math.abs(slot.x) <= YARD.bedWidth / 2, 'crate must stay between the side walls');
      assert.ok(Math.abs(slot.z - YARD.bedZ) <= deck / 2, 'crate must stay on the deck length');
      assert.ok(slot.y >= 0.8, 'crate must sit above the deck floor');
      // Because the offsets are relative to the truck, they move with it.
      const entering = toWorld(slot, truckPose(0, 1, length));
      const docked = toWorld(slot, truckPose(1, 1, length));
      assert.ok(Math.abs(entering.x - docked.x) > 1, 'cargo has to travel with the vehicle');
      assert.ok(docked.z >= YARD.tailZ - 1e-9 && docked.z <= truckBayZ(length) + length / 2, 'docked cargo stays inside the vehicle footprint');
    }
    // The row nearest the tail ends up over the unloading dock, not out on the road.
    const rear = toWorld(bedCargo(0, length), truckPose(1, 1, length));
    assert.ok(rear.z >= YARD.tailZ - 1e-9 && rear.z <= YARD.dockFront, 'nearest crate over the dock slab');
    assert.notDeepEqual(bedCargo(0, length), bedCargo(1, length));
    assert.deepEqual(bedCargo(0, length), bedCargo(0, length));
  }
});

test('carriers shuttle between tail and bench with a visible working phase', async () => {
  const { CARRY, carryCycle, carryPeriod, carryWalkTime, YARD } = await load();
  const period = carryPeriod();
  const walk = carryWalkTime();
  assert.equal(carryCycle(0).z, YARD.pickupZ);
  assert.deepEqual(carryCycle(0), carryCycle(period), 'the cycle repeats instead of drifting');
  const carrying = carryCycle(CARRY.pickup + walk / 2);
  assert.equal(carrying.carrying, true);
  assert.equal(carrying.walking, true);
  assert.equal(carrying.facing, Math.PI, 'the carrier faces the bench while loaded');
  assert.ok(carrying.z < YARD.pickupZ && carrying.z > YARD.standZ);
  const working = carryCycle(CARRY.pickup + walk + CARRY.work / 2);
  assert.equal(working.working, true);
  assert.equal(working.walking, false);
  assert.equal(working.z, YARD.standZ);
  const returning = carryCycle(CARRY.pickup + walk + CARRY.work + walk / 2);
  assert.equal(returning.carrying, false, 'the return leg is empty handed');
  assert.equal(returning.walking, true);
  assert.equal(returning.facing, 0);
  for (let t = 0; t < period * 2; t += 0.05) {
    const step = carryCycle(t);
    assert.ok(step.z >= YARD.standZ - 1e-9 && step.z <= YARD.pickupZ + 1e-9, 'carriers never leave the route');
    assert.ok(!(step.carrying && step.working), 'a crate is either carried or being worked on');
  }
  // Per-agent bench: the work stop uses that agent's standZ.
  const deep = YARD.standZ - 1.5;
  assert.equal(carryCycle(CARRY.pickup + carryWalkTime(deep) + CARRY.work / 2, deep).z, deep);
});

test('benchGrid packs agents into a mesh without capping count', async () => {
  const { benchGrid, YARD } = await load();
  assert.equal(benchGrid(0).slots.length, 0);
  assert.equal(benchGrid(1).slots.length, 1);
  assert.equal(benchGrid(1).slots[0].x, 0);
  const nine = benchGrid(9);
  assert.equal(nine.slots.length, 9);
  assert.equal(nine.cols, 3);
  assert.equal(nine.rows, 3);
  assert.ok(nine.slots[3].z < YARD.benchZ, 'extra rows grow toward the warehouse');
  const many = benchGrid(17);
  assert.equal(many.slots.length, 17);
  assert.ok(many.cols <= 4);
  const xs = new Set(many.slots.map(s => s.x));
  assert.ok(xs.size > 1, 'mesh spreads on X');
});

test('polling the same snapshot never rebuilds or restarts the yard', async () => {
  const { sceneSignature } = await load();
  const tasks = [
    { project_id: 'p', id: 'RF-1', status: 'in_progress', owner: 'cursor-goalagents-2', title: 'a' },
    { project_id: 'p', id: 'RF-2', status: 'ready', owner: '', title: 'b' },
  ];
  const agents = [{ project_id: 'p', id: 'cursor-goalagents-2', status: 'working', task_id: 'RF-1' }];
  assert.equal(sceneSignature(tasks, agents, 'en'), sceneSignature([...tasks].reverse(), agents, 'en'));
  assert.equal(sceneSignature(tasks, agents, 'zh'), sceneSignature([...tasks].reverse(), agents, 'zh'));
  assert.notEqual(sceneSignature(tasks, agents, 'en'), sceneSignature(tasks, agents, 'zh'));
  const moved = [{ ...tasks[0], status: 'in_review' }, tasks[1]];
  assert.notEqual(sceneSignature(tasks, agents, 'en'), sceneSignature(moved, agents, 'en'));
  assert.notEqual(sceneSignature(tasks, agents, 'en'), sceneSignature(tasks, [], 'en'));
});

test('the renderer reuses state, projects labels and never writes task state', () => {
  const source = fs.readFileSync(path.resolve(__dirname, 'factory.ts'), 'utf8');
  // Decorative only: no transport, no task writes from the animation loop.
  assert.ok(!/fetch\(|XMLHttpRequest|sendBeacon/.test(source));
  assert.ok(!/method:\s*['"](POST|PUT|PATCH|DELETE)/i.test(source));
  // Reduced motion and hidden views stay supported.
  assert.ok(/prefers-reduced-motion/.test(source));
  assert.ok(/reduced\.matches/.test(source));
  assert.ok(/document\.hidden/.test(source));
  // Full nameplates, re-projected every frame so they follow the worker and camera.
  assert.ok(/displayName\(/.test(source));
  assert.ok(!/shortName|slice\(0, 8\)/.test(source));
  assert.ok(/getWorldPosition/.test(source));
  // Polling keeps truck arrival, walk cycles and positions instead of replaying them.
  assert.ok(/truckState/.test(source) && /peopleState/.test(source));
  assert.ok(/sceneSignature\(/.test(source));
  // Waiting cargo is parented under the truck deck so it yaws with the vehicle.
  assert.ok(/truck\.deck|deck/.test(source));
  assert.ok(/crate\(truck\.deck/.test(source));
  // At the station the body yaw stays locked; only arms keep moving.
  assert.ok(/Planted at the station|lock yaw at the bench/.test(source));
  assert.ok(/benchGrid\(/.test(source));
});
