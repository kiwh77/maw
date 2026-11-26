export const WORKERS = ['Codex · 01','Cursor · 02','CodeBuddy · 03'];
export function createSimulation(){
 const wares=[{id:'WARE–024',title:'登录与身份模块',kind:'微型货车',total:4},{id:'WARE–025',title:'工作台与任务中心',kind:'重型货车',total:12}];
 return {time:0,wares:wares.map(w=>({...w,arrived:false,departed:false})),tasks:wares.flatMap((w,wi)=>Array.from({length:w.total},(_,i)=>({id:`${wi+1}-${i+1}`,ware:wi,status:'queued',worker:null,stageTime:0,blockOnce:wi===1&&i===2}))),workers:WORKERS.map((name,i)=>({name,id:i,task:null})),events:[],blocked:false,released:false};
}
function emit(s,text){s.events.unshift({time:s.time,text});s.events=s.events.slice(0,5)}
export function releaseBlock(s){s.released=true;for(const t of s.tasks)if(t.status==='blocked'){t.status='processing';emit(s,`货物 ${t.id} 恢复处理`)}s.blocked=false}
export function step(s,dt){
 if(!Number.isFinite(dt)||dt<0)return;s.time+=dt;
 s.wares.forEach((w,i)=>{if(!w.arrived&&s.time>1+i*3){w.arrived=true;emit(s,`${w.id} 已停靠 ${i+1} 号月台`)}});
 for(const worker of s.workers){
  if(worker.task===null){const t=s.tasks.find(t=>t.status==='queued'&&s.wares[t.ware].arrived);if(t){t.worker=worker.id;worker.task=t.id;t.status='carrying';t.stageTime=0;emit(s,`${worker.name} 领取货物 ${t.id}`)}}
  const t=s.tasks.find(t=>t.id===worker.task);if(!t)continue;
  if(t.status==='blocked')continue;t.stageTime+=dt;
  if(t.status==='carrying'&&t.stageTime>=3){t.status='processing';t.stageTime=0;}
  else if(t.status==='processing'&&t.stageTime>=4){if(t.blockOnce&&!s.released){t.status='blocked';t.blockOnce=false;s.blocked=true;emit(s,`货物 ${t.id} 缺少验收资料，等待处理`)}else{t.status='storing';t.stageTime=0}}
  else if(t.status==='storing'&&t.stageTime>=3){t.status='stored';worker.task=null;emit(s,`货物 ${t.id} 已验收入库`)}
 }
 s.wares.forEach((w,i)=>{if(!w.departed&&s.tasks.filter(t=>t.ware===i).every(t=>t.status==='stored')){w.departed=true;w.departedAt=s.time;emit(s,`${w.id} 全部入库，完成离厂`)}});
}
