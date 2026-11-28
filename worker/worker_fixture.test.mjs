import { webcrypto } from 'node:crypto';
import readline from 'node:readline';
import { Room } from './worker.js';
Object.defineProperty(globalThis, 'crypto', { value: webcrypto });
const entries = new Map(), sockets = new Map();
let alarm = null;
const storage = {
 async get(k) { return structuredClone(entries.get(k)); },
 async put(k,v) { entries.set(k,structuredClone(v)); },
 async delete(k) { for(const key of Array.isArray(k)?k:[k])entries.delete(key); },
 async list({prefix='' }={}) { return new Map([...entries].filter(([k])=>k.startsWith(prefix)).map(([k,v])=>[k,structuredClone(v)])); },
 async transaction(fn) { return fn(storage); },
 async setAlarm(at) { alarm = at; },
 async getAlarm() { return alarm; },
 async deleteAlarm() { alarm = null; },
};
const state = {storage,id:{toString:()=> 'fixture-room'},getWebSockets:()=>[...sockets.values()].filter(s=>!s.closed)};
// Local integration tests may supply real fixture TURN endpoints. No external
// broker or credentials are used; hibernation retains the same test binding.
const env = process.env.HOLE_FIXTURE_TURN ? {TURN_BROKER:{idFromName:()=> 'fixture',get:()=>({fetch:async()=>Response.json({ice_servers:JSON.parse(process.env.HOLE_FIXTURE_TURN),expire_at:Date.now()+3600000,refresh_at:Date.now()+3000000})})}} : {};
let room = new Room(state,env);
let chain = Promise.resolve();
let dropUpgradeEstablished = process.env.HOLE_FIXTURE_DROP_UPGRADE_ESTABLISHED === '1';
// The streaming harness is driven by Go integration tests. node --test loads
// this file with a protocol pipe on stdin, which must not be consumed here.
const inputLines = process.env.NODE_TEST_CONTEXT ? [] : readline.createInterface({input:process.stdin,crlfDelay:Infinity});
for await (const line of inputLines) {
 chain = chain.then(async()=>{
  const input=JSON.parse(line);
  if(input.kind==='open') {
   const ws={id:input.id,attachment:null,closed:false,serializeAttachment(a){this.attachment=structuredClone(a)},deserializeAttachment(){return structuredClone(this.attachment)},send(data){
    if(process.env.HOLE_FIXTURE_DROP_PARALLEL_DIRECT_ICE==='1') {
     const m=JSON.parse(data),r=[...room.ice.records.values()].find(r=>r.id===m.transport_id);
     if(['ice_description','ice_candidate'].includes(m.type) && r?.upgrade?.bootstrap && m.transport_generation===r.generation) return;
    }
    if(!this.closed)process.stdout.write(JSON.stringify({id:this.id,data})+'\n');
   },close(code,reason){this.closed=true;process.stdout.write(JSON.stringify({id:this.id,close:{code,reason}})+'\n')}};
   // Model unequal signaling latency without reordering frames on a socket.
   if(process.env.HOLE_FIXTURE_COMMIT_DELAY) {
    const deliver=ws.send.bind(ws);let pending=Promise.resolve(),delayed=false;
    ws.send=data=>{
     const m=JSON.parse(data);
     if(ws.attachment?.device_name==='beta' && m.type==='transport_ready' && m.phase==='direct' && m.upgrade_commit && !delayed) {
      delayed=true;pending=pending.then(()=>new Promise(resolve=>setTimeout(resolve,Number(process.env.HOLE_FIXTURE_COMMIT_DELAY))));
     }
     pending=pending.then(()=>deliver(data));
    };
   }
   sockets.set(input.id,ws);await room.loadState();await room.ice.setRoomName(input.room);return;
  }
  if(input.kind==='hibernate') {room=new Room(state,env);return;}
  const ws=sockets.get(input.id);if(!ws)return;
  if(input.kind==='close'){ws.closed=true;await room.webSocketClose(ws,1000,'',true);sockets.delete(input.id);}
  else {
   if(dropUpgradeEstablished && ws.attachment?.device_name==='beta') {
    const m=JSON.parse(input.data),r=[...room.ice.records.values()].find(r=>r.id===m.transport_id);
    if(m.type==='transport_established' && m.path_type==='direct' && r?.upgrade?.committed) {
     // Lose the final report with its socket; the reconnect must replay
     // the persisted commit and obtain another installation acknowledgement.
     dropUpgradeEstablished=false;ws.close(1012,'fixture lost installation report');return;
    }
   }
   await room.webSocketMessage(ws,input.data);
  }
 }).catch(error=>{process.stderr.write(String(error.stack)+'\n');process.exitCode=1;});
}
await chain;
