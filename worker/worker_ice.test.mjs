import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { Room } from './worker.js';
import { TurnBroker, ICE_PROFILE, LEGACY_PROFILE, RELAY_POLICY, negotiateProfile, iceJoinFields, validateTurnResponse } from './worker_ice.mjs';
Object.defineProperty(globalThis,'crypto',{value:webcrypto});
function fixture(){
 const values=new Map(),sockets=[];
 let alarm=null;const counts={puts:0,lists:0,alarms:0};
 const storage={async get(k){return structuredClone(values.get(k))},async put(k,v){counts.puts++;values.set(k,structuredClone(v))},async delete(keys){for(const k of Array.isArray(keys)?keys:[keys])values.delete(k)},async list({prefix='' }={}){counts.lists++;return new Map([...values].filter(([k])=>k.startsWith(prefix)).map(([k,v])=>[k,structuredClone(v)]))},async transaction(fn){return fn(storage)},async setAlarm(at){counts.alarms++;alarm=at},async getAlarm(){return alarm},async deleteAlarm(){alarm=null}};
 const state={storage,id:{toString:()=> 'fixture-room'},getWebSockets:()=>sockets.filter(s=>!s.closed)};
 function socket(){const s={attachment:null,closed:false,messages:[],serializeAttachment(v){this.attachment=structuredClone(v)},deserializeAttachment(){return structuredClone(this.attachment)},send(v){this.messages.push(JSON.parse(v))},close(){this.closed=true}};sockets.push(s);return s}
 return{values,state,socket,counts};
}
const endpoint={protocol:'tcp',addr:'127.0.0.1',port:22};
function join(name,provide=[],consume=[]){return{type:'join',signal_version:2,auth_mode:'shared-secret',runtime_id:(name==='alpha'?'a':'b').repeat(32),transport_epoch:'1',transport_profiles:[ICE_PROFILE,LEGACY_PROFILE],session_versions:[2],cert_fingerprint:(name==='alpha'?'a':'b').repeat(64),room:'fixture',token:'room-secret',device_name:name,provide:provide.map(id=>({id,service:endpoint})),consume:consume.map(id=>({id,expose:{addr:'127.0.0.1',port:18080}}))}}
async function pair(){const f=fixture();const r=new Room(f.state,{});const a=f.socket(),b=f.socket();await r.webSocketMessage(a,JSON.stringify(join('alpha',['one','two'])));await r.webSocketMessage(b,JSON.stringify(join('beta',[],['one','two'])));return{...f,r,a,b,record:[...r.ice.records.values()][0]}}
const payload={iceServers:[{urls:['stun:stun.cloudflare.com:3478']},{urls:['turn:turn.cloudflare.com:3478?transport=udp','turn:turn.cloudflare.com:3478?transport=tcp','turn:turn.cloudflare.com:80?transport=tcp','turns:turn.cloudflare.com:5349?transport=tcp','turns:turn.cloudflare.com:443?transport=tcp'],username:'temporary-user',credential:'temporary-secret'}]};

test('version negotiation only falls back when both sides advertise legacy',()=>{
 assert.equal(negotiateProfile({transport_profiles:[ICE_PROFILE]},{transport_profiles:[ICE_PROFILE]}),ICE_PROFILE);
 assert.equal(negotiateProfile({transport_profiles:[ICE_PROFILE]},{}),null);
 assert.equal(negotiateProfile({transport_profiles:[ICE_PROFILE,LEGACY_PROFILE]},{}),LEGACY_PROFILE);
 assert.equal(iceJoinFields({...join('alpha'),auth_mode:'device-proof'}),null);
 assert.equal(iceJoinFields({...join('alpha'),transport_epoch:1}),null);
});
test('one persisted pair carries multiple mappings and sorted initiator',async()=>{const{record}=await pair();assert.equal(record.mappings.length,2);assert.equal(record.a,'alpha');assert.equal(record.generation,'1')});
test('concurrent restarts of one generation coalesce',async()=>{
 const {r,a,b,record}=await pair();const message={type:'transport_restart',request_id:'restart',transport_id:record.id,expected_generation:'1',phase:'relay_udp'};
 await r.webSocketMessage(a,JSON.stringify(message));await r.webSocketMessage(b,JSON.stringify(message));
 assert.equal(record.generation,'2');assert.equal(record.phase,'relay_udp');
});
test('removing one mapping does not replace healthy peer generation',async()=>{
 const {r,a,b,record,socket}=await pair();const next=socket();await r.webSocketMessage(next,JSON.stringify(join('beta',[],['two'])));
 assert.equal(record.generation,'1');assert.equal(record.mappings.length,1);assert.equal(record.mappings[0].mapping_id,'two');assert.equal(b.closed,true);
});
test('candidate routing validates socket ownership, peer, generation and limit',async()=>{
 const {r,a,b,record}=await pair();const base={type:'ice_candidate',request_id:'candidate',transport_id:record.id,transport_generation:'1',to_peer_id:'beta',candidate:'1 1 udp 123 127.0.0.1 15000 typ host'};
 await r.webSocketMessage(a,JSON.stringify({...base,to_peer_id:'stranger'}));assert.equal(a.messages.at(-1).code,'transport_not_authorized');
 const n=b.messages.length;await r.webSocketMessage(a,JSON.stringify({...base,transport_generation:'0'}));assert.equal(b.messages.length,n);
 await r.webSocketMessage(a,JSON.stringify({...base,from_peer_id:'spoofed'}));assert.equal(b.messages.at(-1).peer_device,'alpha');
 for(let i=1;i<65;i++)await r.webSocketMessage(a,JSON.stringify({...base,candidate:`${i+1} 1 udp 123 127.0.0.1 ${15000+i} typ host`}));
 assert.equal(record.ice.alpha.candidates.length,64);assert.equal(a.messages.at(-1).code,'candidate_limit');
});
test('hibernation restores generations and replays cached description first',async()=>{
 const f=await pair();const r=f.r;const base={request_id:'x',transport_id:f.record.id,transport_generation:'1',to_peer_id:'beta'};
 await r.webSocketMessage(f.a,JSON.stringify({...base,type:'ice_candidate',candidate:'1 1 udp 1 127.0.0.1 12345 typ host'}));
 await r.webSocketMessage(f.a,JSON.stringify({...base,type:'ice_description',ufrag:'abcd',pwd:'x'.repeat(32)}));
 const resumed=new Room(f.state,{});const before=f.b.messages.length;await resumed.webSocketMessage(f.b,JSON.stringify({type:'transport_sync'}));
 const messages=f.b.messages.slice(before);assert.equal(messages[0].type,'transport_ready');assert.equal(messages[1].type,'ice_description');assert.equal(messages[2].type,'ice_candidate');assert.equal(messages[0].transport_generation,'1');
});
test('zero online members preserves room authentication and transport records',async()=>{
 const f=await pair();await f.r.webSocketClose(f.a);f.a.closed=true;await f.r.webSocketClose(f.b);f.b.closed=true;
 assert.equal(f.values.get('token'),'room-secret');assert.ok([...f.values.keys()].some(k=>k.startsWith('transport:')));
 const later=new Room(f.state,{}),c=f.socket();await later.webSocketMessage(c,JSON.stringify({...join('alpha'),token:'different'}));assert.equal(c.messages.at(-1).code,'invalid_token');
});
test('Cloudflare response accepts documented object/array shapes and excludes non-primary ports',()=>{
 assert.equal(validateTurnResponse(payload).length,1);
 assert.equal(validateTurnResponse({iceServers:payload.iceServers[1]}).length,1);
 assert.throws(()=>validateTurnResponse({iceServers:[{urls:['turn:HOST:53'],username:'a',credential:'b'}]}));
 assert.throws(()=>validateTurnResponse({iceServers:[{urls:['turn:HOST:3478'],username:'a'}]}));
 assert.deepEqual(validateTurnResponse(payload)[0].urls, ['turn:turn.cloudflare.com:3478?transport=udp','turn:turn.cloudflare.com:80?transport=tcp','turn:turn.cloudflare.com:3478?transport=tcp','turns:turn.cloudflare.com:443?transport=tcp','turns:turn.cloudflare.com:5349?transport=tcp']);
 for(const url of ['turns:HOST:443?transport=udp','turn:HOST:80?transport=udp','turn:USER@HOST:3478?transport=tcp','turn:HOST:53?transport=udp']) assert.throws(()=>validateTurnResponse({iceServers:[{urls:[url],username:'a',credential:'b'}]}));
});

test('new relay order is negotiated by both endpoints and persists through hibernation',async()=>{
 const f=fixture(), r=new Room(f.state,{}), a=f.socket(), b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY}));
 const record=[...r.ice.records.values()][0]; assert.equal(record.relay_policy,RELAY_POLICY);
 for(const phase of ['relay_udp','relay_tcp_80','relay_tcp','relay_tls_443','relay_tls','direct']) {
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase}));
  assert.equal(record.phase,phase); assert.equal(b.messages.at(-1).relay_policy,RELAY_POLICY);
 }
 const resumed=new Room(f.state,{}); await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.equal(b.messages.at(-1).relay_policy,RELAY_POLICY); assert.equal(b.messages.at(-1).transport_generation,record.generation);
});
test('mixed clients retain old stages instead of sending an unsupported TCP phase',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY}));
 await r.webSocketMessage(b,JSON.stringify(join('beta',[],['one'])));
 const record=[...r.ice.records.values()][0]; assert.equal(record.relay_policy,'');
 for(const phase of ['relay_udp','relay_tls','relay_tls_443']) {
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase}));
  assert.equal(record.phase,phase);
 }
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_tcp_80'}));
 assert.equal(a.messages.at(-1).code,'invalid_phase');
 assert.equal(iceJoinFields({...join('alpha'),relay_policy:'unknown'}),null);
});
test('new relay policy rejects skipped protocol and port stages',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY}));
 const record=[...r.ice.records.values()][0];
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_tcp'}));
 assert.equal(a.messages.at(-1).code,'invalid_phase');assert.equal(record.generation,'1');
});
test('TURN requests coalesce and cached credentials survive broker reconstruction',async()=>{
 const f=fixture(),env={TURN_KEY_ID:'key',TURN_KEY_API_TOKEN:'MASTER_ONLY'};let calls=0;const original=globalThis.fetch;
 globalThis.fetch=async(_url,options)=>{calls++;assert.equal(options.headers.Authorization,'Bearer MASTER_ONLY');await new Promise(r=>setTimeout(r,10));return Response.json(payload)};
 try{const broker=new TurnBroker(f.state,env);const request=()=>new Request('https://turn-broker/credentials',{method:'POST',body:JSON.stringify({room:'r',device:'alpha',ttl:21600})});
 const responses=await Promise.all([broker.fetch(request()),broker.fetch(request()),broker.fetch(request())]);const bodies=await Promise.all(responses.map(r=>r.text()));assert.equal(calls,1);assert.equal(new Set(bodies).size,1);assert.ok(!bodies[0].includes('MASTER_ONLY'));
 await new TurnBroker(f.state,env).fetch(request());assert.equal(calls,1);
 }finally{globalThis.fetch=original}
});
test('TURN unavailable is explicit and never returns fake empty credentials',async()=>{const f=fixture();const r=await new TurnBroker(f.state,{}).fetch(new Request('https://turn-broker/credentials',{method:'POST',body:'{}'}));assert.equal(r.status,503);assert.equal((await r.json()).code,'relay_unavailable')});
test('late TURN response is discarded after requesting socket is replaced',async()=>{
 const f=await pair();let release;const done=new Promise(r=>{release=r});f.r.ice.env.TURN_BROKER={idFromName:()=>1,get:()=>({fetch:async()=>{await done;return Response.json({ice_servers:payload.iceServers,expire_at:Date.now()+600000,refresh_at:Date.now()+480000})}})};
 const request=f.r.webSocketMessage(f.a,JSON.stringify({type:'turn_request',request_id:'pending',ttl:600}));await new Promise(r=>setTimeout(r,0));const next=f.socket();await f.r.webSocketMessage(next,JSON.stringify({...join('alpha',['one','two']),runtime_id:'c'.repeat(32),cert_fingerprint:'c'.repeat(64)}));release();await request;
 assert.ok(!next.messages.some(m=>m.type==='turn_servers'));assert.ok(!f.a.messages.some(m=>m.type==='turn_servers'));
});

test('lightweight renewal does not write topology or replay ICE after hibernation',async()=>{
 const f=await pair();assert.equal(f.a.messages.find(m=>m.type==='joined').lease_renewal,true);
 const base={transport_id:f.record.id,transport_generation:'1',to_peer_id:'alpha'};
 await f.r.webSocketMessage(f.b,JSON.stringify({...base,type:'ice_description',ufrag:'abcd',pwd:'x'.repeat(32)}));
 await f.r.webSocketMessage(f.b,JSON.stringify({...base,type:'ice_candidate',candidate:'1 1 udp 1 127.0.0.1 12345 typ host'}));
 const writes=f.counts.puts;
 for(const room of [f.r,new Room(f.state,{})]) {
  const before=f.a.messages.length;
  await room.webSocketMessage(f.a,JSON.stringify({type:'transport_renew',request_id:'renew'}));
  const messages=f.a.messages.slice(before);assert.equal(messages.length,1);assert.equal(messages[0].type,'transport_lease');
  assert.equal(messages[0].leases[0].transport_id,f.record.id);assert.equal(messages[0].leases[0].transport_generation,'1');
  assert.ok(Number(messages[0].leases[0].lease_until)>Date.now());assert.equal(f.counts.puts,writes);
 }
 assert.equal(await f.state.storage.getAlarm(),null);
});
test('online transport survives more than 24 hours and cold reconstruction',async(t)=>{
 const f=await pair(),original=f.record.id;const now=Date.now()+25*60*60*1000;t.mock.method(Date,'now',()=>now);
 const writes=f.counts.puts;const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(f.a,JSON.stringify({type:'transport_renew'}));
 assert.equal(resumed.ice.records.size,1);assert.equal([...resumed.ice.records.values()][0].id,original);
 assert.equal(f.a.messages.at(-1).leases[0].transport_id,original);assert.equal(f.counts.puts,writes);
});
test('offline retention starts at disconnect and expires without another user request',async(t)=>{
 const f=await pair();let now=Date.now()+25*60*60*1000;t.mock.method(Date,'now',()=>now);
 f.a.closed=true;await f.r.webSocketClose(f.a);f.b.closed=true;await f.r.webSocketClose(f.b);
 assert.equal(f.record.expires_at,now+24*60*60*1000);assert.equal(await f.state.storage.getAlarm(),f.record.expires_at);
 const writes=f.counts.puts;await f.r.reconcile();assert.equal(f.counts.puts,writes);
 now=f.record.expires_at+1;await f.state.storage.deleteAlarm();await new Room(f.state,{}).alarm();
 assert.equal([...f.values.keys()].filter(k=>k.startsWith('transport:')).length,0);
 assert.equal(f.values.get('token'),'room-secret');assert.equal(await f.state.storage.getAlarm(),null);
});
test('broker schedules actual rate/cache expiries and stops after the final deletion',async(t)=>{
 const f=fixture();let now=Date.now();t.mock.method(Date,'now',()=>now);
 t.mock.method(globalThis,'fetch',async()=>Response.json(payload));
 const broker=new TurnBroker(f.state,{TURN_KEY_ID:'key',TURN_KEY_API_TOKEN:'TOKEN'});
 const response=await broker.fetch(new Request('https://turn-broker/credentials',{method:'POST',body:JSON.stringify({room:'fixture',device:'alpha',ttl:21600})}));
 assert.equal(response.status,200);assert.equal(await f.state.storage.getAlarm(),now+120000);
 let ticks=0;
 while(await f.state.storage.getAlarm()!==null&&ticks<10) {
  now=await f.state.storage.getAlarm();await f.state.storage.deleteAlarm();await new TurnBroker(f.state,{}).alarm();ticks++;
 }
 assert.equal(ticks,3);assert.equal(f.counts.lists,6);assert.equal(f.values.size,0);assert.equal(await f.state.storage.getAlarm(),null);
});
