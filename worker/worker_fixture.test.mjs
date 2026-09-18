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
let room = new Room(state,{});
let chain = Promise.resolve();
for await (const line of readline.createInterface({input:process.stdin,crlfDelay:Infinity})) {
 chain = chain.then(async()=>{
  const input=JSON.parse(line);
  if(input.kind==='open') {
   const ws={id:input.id,attachment:null,closed:false,serializeAttachment(a){this.attachment=structuredClone(a)},deserializeAttachment(){return structuredClone(this.attachment)},send(data){if(!this.closed)process.stdout.write(JSON.stringify({id:this.id,data})+'\n')},close(code,reason){this.closed=true;process.stdout.write(JSON.stringify({id:this.id,close:{code,reason}})+'\n')}};
   sockets.set(input.id,ws);await room.loadState();await room.ice.setRoomName(input.room);return;
  }
  if(input.kind==='hibernate') {room=new Room(state,{});return;}
  const ws=sockets.get(input.id);if(!ws)return;
  if(input.kind==='close'){ws.closed=true;await room.webSocketClose(ws,1000,'',true);sockets.delete(input.id);}
  else await room.webSocketMessage(ws,input.data);
 }).catch(error=>{process.stderr.write(String(error.stack)+'\n');process.exitCode=1;});
}
await chain;
