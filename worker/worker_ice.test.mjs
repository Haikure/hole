import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto, timingSafeEqual } from 'node:crypto';
import worker, { Room } from './worker.js';
import { panelPath, PANEL_SESSION_NAME } from './worker_panel.mjs';
import { PANEL_PAGE } from './worker_panel_page.mjs';
import { runInNewContext } from 'node:vm';
import { TurnBroker, ICE_PROFILE, FIXED_PROFILE, RELAY_POLICY, negotiateProfile, iceJoinFields, validateTurnResponse, relayPairs, validFixedEndpoint } from './worker_ice.mjs';
import {HOLE_VERSION,deviceRelayPlan,validNATPlan,validPunch} from './worker_ice.mjs';
import { roomSigningBytes } from './worker_room.mjs';

test('room directory advertises bounded service IDs without exposing endpoints or accepting injected metadata', async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const names=Array.from({length:64},(_,i)=>`service-${String(i).padStart(2,'0')}`);
 await r.webSocketMessage(a,JSON.stringify({...published(join('alpha',names)),services:[{id:'forged',protocol:'tcp'}]}));
 await r.webSocketMessage(b,JSON.stringify(join('beta')));
 const host=r.control.snapshot().members.find(m=>m.device_name==='alpha');
 assert.equal(host.services.length,32);
 assert.deepEqual(host.services[0],{id:'service-00',protocol:'tcp',kind:'minecraft'});
 assert.equal(JSON.stringify(host.services).includes('127.0.0.1'),false);
 assert.equal(JSON.stringify(host.services).includes('forged'),false);
 assert.equal(r.ice.records.size,0);
 const declaration=r.members.get('alpha').member;
 declaration.provide=names.map(id=>({id:'世'.repeat(50)+id,service:endpoint,discovery:'minecraft'}));
 const bounded=r.control.snapshot().members.find(m=>m.device_name==='alpha').services;
 assert.ok(bounded.length>0 && bounded.length<32);
 assert.ok(new TextEncoder().encode(JSON.stringify(bounded)).length<=2048);
});

test('service directory survives hibernation without polling writes or new alarms',async()=>{
 const f=await modernFixture(),host=f.socket(),viewer=f.socket();
 await f.r.webSocketMessage(host,JSON.stringify(published(modernJoin('alpha',['minecraft.world']))));
 await f.r.webSocketMessage(viewer,JSON.stringify(modernJoin('beta')));
 const expected=[{id:'minecraft.world',protocol:'tcp',kind:'minecraft'}];
 const directory=r=>r.control.snapshot().members.find(m=>m.device_name==='alpha').services;
 assert.deepEqual(directory(f.r),expected);
 assert.equal(f.r.ice.records.size,0);
 const writes=f.counts.puts,alarms=f.counts.alarms,messages=viewer.messages.length;
 for(let i=0;i<5;i++) await f.r.control.publishSnapshot();
 assert.equal(f.counts.puts,writes);
 assert.equal(f.counts.alarms,alarms);
 assert.equal(viewer.messages.length,messages);
 const resumed=f.newRoom();
 await resumed.loadState();
 assert.deepEqual(directory(resumed),expected);
 assert.equal(f.counts.alarms,alarms);
 assert.deepEqual(f.values.get('member:alpha').provide,[{id:'minecraft.world',service:endpoint,discovery:'minecraft'}]);
 assert.equal(JSON.stringify(host.deserializeAttachment()).includes('minecraft.world'),false);
 await resumed.control.publishSnapshot();
 const frame=viewer.messages.filter(m=>m.type==='room_frame').at(-1).room_frame;
 const body=JSON.parse(Buffer.from(frame.body,'base64').toString());
 assert.deepEqual(body.members.find(m=>m.device_name==='alpha').services,expected);
 const restored=f.newRoom();
 await restored.webSocketMessage(host,JSON.stringify(memberUpdate(modernJoin('alpha'),1)));
 assert.deepEqual(directory(restored),[]);
 assert.equal(restored.ice.records.size,0);
 assert.equal(f.counts.alarms,alarms);
});

test('publishing Minecraft preserves ordinary mappings and legacy room messages across hibernation',async()=>{
 const f=await modernFixture(),host=f.socket(),ordinary=f.socket(),legacyViewer=f.socket();
 const ids=Array.from({length:40},(_,i)=>`service-${String(i).padStart(2,'0')}`);
 const declaration={...modernJoin('alpha',ids),live_config:true};
 await f.r.webSocketMessage(host,JSON.stringify(declaration));
 await f.r.webSocketMessage(ordinary,JSON.stringify({...modernJoin('beta',[],ids),live_config:true}));
 await f.r.webSocketMessage(legacyViewer,JSON.stringify(join('legacy')));
 const record=[...f.r.ice.records.values()][0],generation=record.generation;
 assert.equal(record.mappings.length,40); // Discovery limits never truncate forwarding.
 await f.r.webSocketMessage(host,JSON.stringify(memberUpdate({...declaration,provide:[...declaration.provide,{id:'minecraft.world',service:endpoint,discovery:'minecraft'}]},1)));
 assert.deepEqual(f.r.control.snapshot().members.find(m=>m.device_name==='alpha').services,[{id:'minecraft.world',protocol:'tcp',kind:'minecraft'}]);
 assert.equal(record.generation,generation);
 assert.deepEqual(record.mappings.map(m=>m.mapping_id).sort(),ids);
 const legacy=legacyViewer.messages.filter(m=>m.type==='room_state').at(-1);
 assert.deepEqual(legacy.members,[{device_name:'alpha',voice:false},{device_name:'beta',voice:false},{device_name:'legacy',voice:false}]);
 const resumed=f.newRoom();
 await resumed.webSocketMessage(ordinary,JSON.stringify({type:'transport_sync'}));
 const recovered=[...resumed.ice.records.values()][0];
 assert.equal(recovered.id,record.id);
 assert.equal(recovered.generation,generation);
 assert.deepEqual(recovered.mappings.map(m=>m.mapping_id).sort(),ids);
 await resumed.webSocketMessage(host,JSON.stringify(memberUpdate(declaration,2)));
 assert.equal(recovered.generation,generation);
 assert.equal(recovered.mappings.length,40);
 assert.equal(host.closed,false);
 assert.equal(ordinary.closed,false);
});

test('ordinary registrations stay private to discovery and explicit Minecraft publication reaches the panel',async()=>{
 const f=await modernFixture(),host=f.socket(),viewer=f.socket();
 const declaration={...modernJoin('alpha',['ssh','minecraft.0123456789abcdef.V29ybGQ']),live_config:true};
 await f.r.webSocketMessage(host,JSON.stringify(declaration));
 await f.r.webSocketMessage(viewer,JSON.stringify(modernJoin('beta')));
 const services=r=>r.control.snapshot().members.find(m=>m.device_name==='alpha').services;
 assert.deepEqual(services(f.r),[]);
 assert.deepEqual(f.r.panelSnapshot().members.find(m=>m.device_name==='alpha').services,[]);
 const advertised={...declaration,provide:declaration.provide.map((p,i)=>i?{...p,discovery:'minecraft'}:p)};
 await f.r.webSocketMessage(host,JSON.stringify(memberUpdate(advertised,1)));
 const expected=[{id:declaration.provide[1].id,protocol:'tcp',kind:'minecraft'}];
 assert.deepEqual(services(f.r),expected);
 const panel=f.r.panelSnapshot();
 assert.deepEqual(panel.members.find(m=>m.device_name==='alpha').services,expected);
 assert.equal(JSON.stringify(panel).includes('127.0.0.1'),false);
 assert.equal(JSON.stringify(panel).includes('ssh'),false);
 const restored=f.newRoom();await restored.loadState();
 assert.deepEqual(restored.panelSnapshot().members.find(m=>m.device_name==='alpha').services,expected);
 await restored.webSocketMessage(host,JSON.stringify(memberUpdate(declaration,2)));
 assert.deepEqual(services(restored),[]);
 assert.equal(restored.members.get('alpha').member.provide.length,2);
 assert.deepEqual(restored.panelSnapshot().members.find(m=>m.device_name==='alpha').services,[]);
});

test('Minecraft metadata follows directory updates and hibernation without exposing extra fields',async()=>{
 const f=await modernFixture(),host=f.socket();
 const info={description:'海岛世界',version:'1.20.1',online:2,max:20};
 const declaration={...published(modernJoin('alpha',['world'])),live_config:true};
 declaration.provide[0].minecraft={...info,private_address:'127.0.0.1',players:['private-name']};
 await f.r.webSocketMessage(host,JSON.stringify(declaration));
 const services=r=>r.control.snapshot().members.find(m=>m.device_name==='alpha').services;
 assert.deepEqual(services(f.r)[0].minecraft,info);
 assert.equal(JSON.stringify(f.r.panelSnapshot()).includes('private-name'),false);
 declaration.provide[0].minecraft={...info,online:0};
 await f.r.webSocketMessage(host,JSON.stringify(memberUpdate(declaration,1)));
 assert.equal(services(f.r)[0].minecraft.online,0);
 const restored=f.newRoom();await restored.loadState();
 assert.deepEqual(services(restored)[0].minecraft,{...info,online:0});
});

test('invalid world metadata does not alter current registrations',async()=>{
 const info={description:'world',version:'1.20.1',online:0,max:20};
 for(const minecraft of [null,{}, {...info,description:'界'.repeat(65)}, {...info,description:'a\nb'}, {...info,version:''}, {...info,online:-1}, {...info,max:1000001}, {...info,online:0.5}]){
  const f=await modernFixture(),host=f.socket(),declaration={...published(modernJoin('alpha',['world'])),live_config:true};
  await f.r.webSocketMessage(host,JSON.stringify(declaration));
  await f.r.webSocketMessage(host,JSON.stringify(memberUpdate({...declaration,provide:[{...declaration.provide[0],minecraft}]},1)));
  assert.equal(host.messages.at(-1).type,'error');
  assert.equal(f.r.control.snapshot().members[0].services[0].minecraft,undefined);
 }
});

test('invalid discovery markers are rejected without changing the registered services',async()=>{
 for(const change of [{discovery:true},{discovery:'unknown'},{discovery:'minecraft',service:{...endpoint,protocol:'udp'}},{discovery:'minecraft',id:'bad\u0085id'}]) {
  const f=await modernFixture(),host=f.socket();
  const declaration={...modernJoin('alpha',['world']),live_config:true};
  await f.r.webSocketMessage(host,JSON.stringify(declaration));
  await f.r.webSocketMessage(host,JSON.stringify(memberUpdate({...declaration,provide:[{...declaration.provide[0],...change}]},1)));
  assert.deepEqual(f.r.members.get('alpha').member.provide,declaration.provide);
  assert.deepEqual(f.r.control.snapshot().members[0].services,[]);
  assert.equal(host.messages.at(-1).type,'error');
 }
});

async function upgradeFixture(extra={}) {
 const f=await modernFixture(),sockets=Object.fromEntries(['alpha','beta','relay'].map(n=>[n,f.socket()]));
 const caps={voice:true,path_upgrade:true,relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1',...extra};
 for(const [name,ws] of Object.entries(sockets)) await f.r.webSocketMessage(ws,JSON.stringify({...modernJoin(name),...caps}));
 const record=[...f.r.ice.records.values()].find(r=>r.a==='alpha' && r.b==='beta');
 const send=(name,m)=>f.r.webSocketMessage(sockets[name],JSON.stringify({transport_id:record.id,transport_generation:record.generation,expected_generation:record.generation,...m}));
 const establish=async(path)=>{for(const n of ['alpha','beta']) await send(n,{type:'transport_established',path_type:path});};
 const legs=async()=>{
  for(const leg of [...f.r.ice.records.values()].filter(r=>r!==record)) for(const name of [leg.a,leg.b]) {
   await f.r.webSocketMessage(sockets[name],JSON.stringify({type:'transport_established',transport_id:leg.id,transport_generation:leg.generation,path_type:'direct'}));
  }
 };
 await send('alpha',{type:'transport_restart',relay_round:1,reason:'direct_exhausted'});
 await establish('relay');
 return {...f,sockets,record,send,establish,legs};
}

test('late Peer Relay availability upgrades an active relay then permits background direct probes',async()=>{
 const f=await upgradeFixture(),{record:r,send,establish,legs}=f;
 const panel=()=>f.r.panelSnapshot().transports.find(t=>t.id===r.id);
 const original=r.generation;
 await legs();
 assert.equal(r.upgrade.active.generation,original);
 assert.equal(r.upgrade.target,'peer_relay');
 assert.ok(r.peer_relay);
 assert.equal(panel().generation,original);
 assert.deepEqual(panel().route,{kind:'turn'});
 assert.deepEqual(panel().established,['relay','relay']);
 assert.deepEqual(panel().upgrade.route,{kind:'peer_relay',device:'relay'});
 const candidate=r.generation;
 await send('alpha',{type:'transport_upgrade_prepared',path_type:'peer_relay'});
 assert.equal(r.upgrade.committed,false);
 await send('beta',{type:'transport_upgrade_prepared',path_type:'peer_relay',transport_generation:original});
 assert.equal(r.upgrade.committed,false);
 await send('beta',{type:'transport_upgrade_prepared',path_type:'relay'});
 assert.equal(r.upgrade.committed,false);
 await send('beta',{type:'transport_upgrade_prepared',path_type:'peer_relay'});
 assert.equal(r.upgrade.committed,true);
 for(const name of ['alpha','beta']) assert.equal(f.sockets[name].messages.filter(m=>m.type==='transport_ready' && m.transport_id===r.id).at(-1).upgrade_commit,true);
 await establish('peer_relay');
 assert.equal(r.upgrade,null);
 assert.deepEqual(panel().route,{kind:'peer_relay',device:'relay'});
 assert.equal(panel().upgrade,null);
 const route=r.peer_relay.route;
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 assert.equal(r.upgrade.active.generation,candidate);
 assert.equal(r.upgrade.active.peer_relay.route,route);
 assert.equal(r.upgrade.target,'direct');
 assert.equal(panel().generation,candidate);
 assert.deepEqual(panel().route,{kind:'peer_relay',device:'relay'});
 assert.deepEqual(panel().established,['peer_relay','peer_relay']);
 assert.equal(panel().upgrade.target,'direct');
 assert.equal(f.sockets.relay.messages.some(m=>m.type==='peer_relay_revoke' && m.peer_relay.route===route),false);
 await send('alpha',{type:'transport_restart',reason:'ice_failed'});
 assert.equal(r.direct_stage,'punch');
 const generation=r.generation;
 await send('beta',{type:'transport_restart',reason:'ice_failed'});
 assert.equal(r.generation,generation);
 await send('alpha',{type:'transport_restart',reason:'upgrade_failed'});
 assert.equal(r.upgrade.idle,true);
 assert.deepEqual(panel().route,{kind:'peer_relay',device:'relay'});
 assert.equal(panel().upgrade.idle,true);
 assert.equal(r.upgrade.active.peer_relay.route,route);
 assert.equal(f.sockets.relay.messages.some(m=>m.type==='peer_relay_revoke' && m.peer_relay.route===route),false);
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 for(const n of ['alpha','beta']) await send(n,{type:'transport_upgrade_prepared',path_type:'direct'});
 await send('alpha',{type:'transport_established',path_type:'direct'});
 assert.ok(r.upgrade,'retain old grant until both install the direct path');
 assert.deepEqual(panel().route,{kind:'peer_relay',device:'relay'});
 assert.deepEqual(panel().upgrade.established,['direct','']);
 await send('beta',{type:'transport_established',path_type:'direct'});
 assert.equal(r.upgrade,null);
 assert.equal(panel().route,null);
 assert.deepEqual(panel().established,['direct','direct']);
 assert.equal(f.sockets.relay.messages.some(m=>m.type==='peer_relay_revoke' && m.peer_relay.route===route),true);
});

test('background upgrade replays a committed decision and drops stale failure messages',async()=>{
 const f=await upgradeFixture(),{record:r,send}=f;
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 const generation=r.generation;
 for(const n of ['alpha','beta']) await send(n,{type:'transport_upgrade_prepared',path_type:'direct'});
 const resumed=f.newRoom();
 await resumed.webSocketMessage(f.sockets.beta,JSON.stringify({type:'transport_sync'}));
 assert.equal(f.sockets.beta.messages.filter(m=>m.type==='transport_ready' && m.transport_id===r.id).at(-1).upgrade_commit,true);
 const restored=[...resumed.ice.records.values()].find(x=>x.id===r.id);
 await resumed.webSocketMessage(f.sockets.alpha,JSON.stringify({type:'transport_restart',transport_id:r.id,expected_generation:String(BigInt(generation)-1n),reason:'upgrade_failed'}));
 assert.equal(restored.generation,generation);
 assert.equal(restored.upgrade.committed,true);
});

test('node relay preempts direct probing without retiring the active relay and backs off after failure',async t=>{
 let now=Date.now();t.mock.method(Date,'now',()=>now);
 const f=await upgradeFixture(),{record:r,send,legs}=f;
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 const first=r.generation,active=r.upgrade.active.generation;
 await legs();
 assert.notEqual(r.generation,first);
 assert.equal(r.upgrade.active.generation,active);
 assert.equal(r.upgrade.target,'peer_relay');
 await send('alpha',{type:'transport_restart',reason:'upgrade_failed'});
 assert.equal(r.upgrade.idle,true);
 await legs();
 assert.equal(r.upgrade.idle,true,'topology replay must not retry the failed node immediately');
 now+=30_001;
 await legs();
 assert.equal(r.upgrade.idle,false);
 assert.equal(r.upgrade.target,'peer_relay');
 await send('beta',{type:'transport_restart',reason:'upgrade_path_lost'});
 assert.equal(r.upgrade,null);
 assert.equal(r.phase,'direct');
});

test('automatic upgrades require bilateral capability and respect relay-only policy',async()=>{
 for(const options of [{path_upgrade:false},{relay_only:true}]) {
  const f=await upgradeFixture(options),{record:r,send,legs}=f;
  const before=r.generation;
  await legs();
  await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
  assert.equal(r.generation,before);
  assert.ok(!r.upgrade);
 }
 const f=await upgradeFixture(),{record:r,send}=f;
 const before=r.generation;
 await send('beta',{type:'transport_restart',reason:'auto_upgrade'});
 assert.equal(r.generation,before,'only initiator schedules direct probes');
});

test('Hole panel route and grant survive probes and hibernation alongside an independent direct relay leg',async()=>{
 const f=await modernFixture(),a=f.socket(),b=f.socket(),h=f.socket();
 await f.r.webSocketMessage(h,JSON.stringify({...modernJoin('hole-node'),voice:true,transport_profiles:[ICE_PROFILE,FIXED_PROFILE],fixed_endpoint:'127.0.0.1:39001',hole_serve:{enabled:true,max_pairs:8,bandwidth_mbps:0}}));
 const caps={path_upgrade:true,relay_policy:RELAY_POLICY,relay_order:['hole'],relay_pairing:'all-pairs-v1',hole_device:'hole-node'};
 await f.r.webSocketMessage(a,JSON.stringify({...modernJoin('alpha',['one']),...caps}));
 await f.r.webSocketMessage(b,JSON.stringify({...modernJoin('beta',[],['one']),...caps,hole_device:undefined,relay_order:['udp'],voice:true,transport_profiles:[ICE_PROFILE,FIXED_PROFILE]}));
 const r=[...f.r.ice.records.values()].find(r=>r.a==='alpha' && r.b==='beta');
 const leg=[...f.r.ice.records.values()].find(r=>r.a==='beta' && r.b==='hole-node');
 for(const ws of [b,h])await f.r.webSocketMessage(ws,JSON.stringify({type:'transport_established',transport_id:leg.id,transport_generation:leg.generation,path_type:'fixed'}));
 const send=(ws,m)=>f.r.webSocketMessage(ws,JSON.stringify({transport_id:r.id,transport_generation:r.generation,expected_generation:r.generation,...m}));
 await send(a,{type:'transport_restart',relay_round:1,reason:'direct_exhausted'});
 for(const ws of [a,b])await send(ws,{type:'transport_established',path_type:'hole'});
 const generation=r.generation,route=r.hole.route;
 // A room graph confirms connectivity, including A-B over Hole; it does not
 // prove that the candidate generation has become a direct transport.
 const frames=await Promise.all([...f.r.members.values()].map(({member})=>f.nodeFrame(member,
  [r,leg].filter(link=>[link.a,link.b].includes(member.device_name)).map(link=>{
   const peer=f.r.members.get(link.a===member.device_name?link.b:link.a).member;
   return {peer_id:peer.device_name,runtime_id:peer.runtime_id,transport_id:link.id,generation:link.generation};
  }))));
 await f.r.webSocketMessage(a,JSON.stringify({type:'room_graph',room_frames:frames}));
 const watch=f.socket();watch.serializeAttachment({panel:'pending'});
 await f.r.webSocketMessage(watch,JSON.stringify({type:'watch',token:'room-secret'}));
 const page=panelGraphFixture();
 const panel=()=>watch.messages.at(-1).transports.find(t=>t.id===r.id);
 const assertHole=()=>{
  const snapshot=watch.messages.at(-1),record=panel();
  assert.equal(snapshot.type,'panel_snapshot');
  assert.equal(record.generation,generation);
  assert.deepEqual(record.route,{kind:'hole',device:'hole-node'});
  assert.deepEqual(record.established,['hole','hole']);
  const edges=[...page.buildEdges(snapshot).values()].filter(e=>e.kind==='transport');
  const segments=edges.filter(e=>e.record?.id===r.id),direct=edges.find(e=>e.record?.id===leg.id);
  assert.equal(segments.length,2);
  assert.ok(segments.every(e=>e.route.kind==='hole' && e.confirmed && e.graphConfirmed));
  assert.equal(direct.route,undefined);
  assert.equal(direct.confirmed,true);
  assert.equal(page.edgeCount(edges),2);
  assert.match(page.edgeLabel(segments[0]),/^Hole 中继/);
  assert.equal(JSON.stringify(snapshot).includes(route),false,'panel must not disclose relay grants');
  assert.equal(JSON.stringify(snapshot).includes('127.0.0.1'),false);
 };
 assertHole();
 await send(a,{type:'transport_restart',reason:'auto_upgrade'});
 assertHole();
 assert.equal(panel().upgrade.target,'direct');
 assert.match(page.phase(panel()),/后台尝试直连/);
 assert.equal(r.upgrade.active.hole.route,route);
 assert.equal(h.messages.some(m=>m.type==='hole_revoke' && m.transport_generation===generation),false);
 await send(a,{type:'transport_restart',reason:'ice_failed'});
 assertHole();
 assert.match(page.phase(panel()),/后台预测打洞/);
 await send(a,{type:'transport_restart',reason:'upgrade_failed'});
 assert.equal(r.upgrade.idle,true);
 assertHole();
 assert.match(page.phase(panel()),/等待重试/);
 const resumed=f.newRoom();
 await resumed.webSocketMessage(h,JSON.stringify({type:'transport_sync'}));
 assertHole();
 assert.equal(h.messages.filter(m=>m.type==='hole_grant').at(-1).hole.route,route);
 const restored=[...resumed.ice.records.values()].find(x=>x.id===r.id);
 const next=(ws,m)=>resumed.webSocketMessage(ws,JSON.stringify({transport_id:restored.id,transport_generation:restored.generation,expected_generation:restored.generation,...m}));
 await next(a,{type:'transport_restart',reason:'auto_upgrade'});
 await next(a,{type:'transport_upgrade_prepared',path_type:'direct'});
 assertHole();
 assert.match(page.phase(panel()),/等待另一端/);
 await next(b,{type:'transport_upgrade_prepared',path_type:'direct'});
 assertHole();
 assert.match(page.phase(panel()),/等待双方确认/);
 await next(a,{type:'transport_established',path_type:'direct'});
 assertHole();
 assert.deepEqual(panel().upgrade.established,['direct','']);
 assert.equal(h.messages.some(m=>m.type==='hole_revoke' && m.transport_generation===generation),false);
 await next(b,{type:'transport_established',path_type:'direct'});
 assert.equal(panel().upgrade,null);
 assert.equal(panel().route,null);
 assert.equal(panel().generation,restored.generation);
 assert.deepEqual(panel().established,['direct','direct']);
 assert.equal([...page.buildEdges(watch.messages.at(-1)).values()].filter(e=>e.record?.id===r.id).length,1);
 assert.equal(h.messages.some(m=>m.type==='hole_revoke' && m.transport_generation===generation),true);
 assert.equal(restored.upgrade,null);
});

test('retained TURN credentials can refresh after an unsuccessful direct probe',async()=>{
 const f=await upgradeFixture(),{record:r,send}=f;
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 await send('alpha',{type:'transport_restart',reason:'upgrade_failed'});
 assert.equal(r.upgrade.idle,true);
 const generation=r.generation;
 await send('alpha',{type:'transport_restart',reason:'turn_refresh',relay_round:1});
 assert.equal(r.upgrade,null);
 assert.notEqual(r.generation,generation);
 assert.equal(r.round,1);
 assert.equal(f.sockets.alpha.messages.filter(m=>m.type==='transport_ready' && m.transport_id===r.id).at(-1).phase,'relay_udp');
});

test('capability removal cancels an upgrade and mixed clients do not start it',async()=>{
 const f=await upgradeFixture(),{record:r,send}=f;
 await send('alpha',{type:'transport_restart',reason:'auto_upgrade'});
 const generation=r.generation;
 await f.r.webSocketMessage(f.sockets.beta,JSON.stringify(memberUpdate({...modernJoin('beta'),voice:true,path_upgrade:false,relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'},1)));
 assert.notEqual(r.generation,generation);
 assert.equal(r.path_upgrade,false);
 assert.equal(r.upgrade,null);
});

test('Peer Relay requires two current direct legs and holds the ordinary fallback round',async()=>{
 for (const evidence of ['missing','relay','stale','direct']) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket(),relay=f.socket();
  const caps={voice:true,peer_relay_version:'peer-relay-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
  for (const [ws,name] of [[a,'alpha'],[b,'beta'],[relay,'relay']]) await r.webSocketMessage(ws,JSON.stringify({...join(name),...caps}));
  const record=[...r.ice.records.values()].find(r=>r.a==='alpha' && r.b==='beta');
  for (const leg of [...r.ice.records.values()].filter(r=>r!==record)) {
   for (const name of [leg.a,leg.b]) {
    if (evidence==='missing' && name==='relay') continue;
    const ws=name==='alpha'?a:name==='beta'?b:relay;
    await r.webSocketMessage(ws,JSON.stringify({type:'transport_established',transport_id:leg.id,
     transport_generation:evidence==='stale'?'0':leg.generation,path_type:evidence==='relay'?'relay':'direct'}));
   }
  }
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,
   expected_generation:record.generation,relay_round:1,reason:'direct_exhausted'}));
  const ready=a.messages.filter(m=>m.type==='transport_ready' && m.transport_id===record.id).at(-1);
  assert.equal(ready.phase,evidence==='direct'?'peer_relay':'relay_udp');
  assert.equal(ready.relay_round,1);
  if (evidence!=='direct') continue;
  const grant=relay.messages.find(m=>m.type==='peer_relay_grant');
  assert.equal(grant.peer_relay.device,'relay');
  assert.equal(grant.peer_relay.links.length,2);
  const generation=record.generation;
  // An unrelated or stale relay failure must not advance this generation.
  await r.webSocketMessage(b,JSON.stringify({type:'peer_relay_failed',transport_id:record.id,transport_generation:generation}));
  assert.equal(record.generation,generation);
  const resumed=new Room(f.state,{});
  await resumed.webSocketMessage(relay,JSON.stringify({type:'transport_sync'}));
  assert.equal(relay.messages.filter(m=>m.type==='peer_relay_grant').at(-1).peer_relay.route,grant.peer_relay.route);
  await resumed.webSocketMessage(relay,JSON.stringify({type:'peer_relay_failed',transport_id:record.id,transport_generation:generation}));
  const fallback=a.messages.filter(m=>m.type==='transport_ready' && m.transport_id===record.id).at(-1);
  assert.equal(fallback.phase,'relay_udp');
  assert.equal(fallback.relay_round,1);
  assert.equal(fallback.peer_relay,undefined);
  assert.equal(relay.messages.some(m=>m.type==='peer_relay_revoke'),true);
 }
});

test('Peer Relay refreshes the persisted route before replaying a live mapping update',async t=>{
 let now=Date.now();
 t.mock.method(Date,'now',()=>now);
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket(),relay=f.socket();
 const caps={voice:true,live_config:true,peer_relay_version:'peer-relay-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 const left={...join('alpha',['one','two']),...caps},right={...join('beta',[],['one','two']),...caps};
 for (const [ws,declaration] of [[a,left],[b,right],[relay,{...join('relay'),...caps}]]) {
  await r.webSocketMessage(ws,JSON.stringify(declaration));
 }
 const record=[...r.ice.records.values()].find(r=>r.a==='alpha' && r.b==='beta');
 for (const leg of [...r.ice.records.values()].filter(r=>r!==record)) {
  for (const [ws,name] of [[a,'alpha'],[b,'beta'],[relay,'relay']]) {
   if (name===leg.a || name===leg.b) await r.webSocketMessage(ws,JSON.stringify({type:'transport_established',
    transport_id:leg.id,transport_generation:leg.generation,path_type:'direct'}));
  }
 }
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,
  expected_generation:record.generation,relay_round:1,reason:'direct_exhausted'}));
 const {route}=record.peer_relay,generation=record.generation;
 const storedLease=Number(f.values.get(`transport:${record.id}`).peer_relay.lease_until);
 now+=5*60_000;
 await r.webSocketMessage(a,JSON.stringify({type:'transport_renew'}));
 assert.equal(Number(f.values.get(`transport:${record.id}`).peer_relay.lease_until),storedLease);
 now+=6*60_000;
 assert.ok(storedLease<now && record.lease_until>now);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify(memberUpdate({...right,consume:right.consume.slice(0,1)},1)));
 const ready=a.messages.filter(m=>m.type==='transport_ready' && m.transport_id===record.id).at(-1);
 const grant=relay.messages.filter(m=>m.type==='peer_relay_grant' && m.transport_id===record.id).at(-1);
 assert.equal(ready.phase,'peer_relay');
 assert.equal(ready.transport_generation,generation);
 assert.equal(ready.peer_relay.route,route);
 assert.deepEqual(ready.mappings.map(m=>m.mapping_id),['one']);
 assert.ok(Number(ready.peer_relay.lease_until)>now);
 assert.equal(ready.peer_relay.lease_until,ready.lease_until);
 assert.deepEqual(grant.peer_relay,ready.peer_relay);
 assert.equal(f.values.get(`transport:${record.id}`).peer_relay.lease_until,ready.lease_until);
});

test('address pool evidence stays bounded and does not assert a global model',()=>{
 const snapshot={status:'complete',type:'address_pool',predictability:'unverified',samples:2,ip_evidence:[
  {ip:'198.51.100.1',targets:1,ports:1,trend:'unknown'},
  {ip:'198.51.100.2',targets:1,ports:1,trend:'unknown'}
 ]};
 assert.equal(validNATPlan({snapshot,addresses:[]}),true);
 for(const ip_evidence of [null,[],[snapshot.ip_evidence[0]],Array(25).fill(snapshot.ip_evidence[0]),
  [snapshot.ip_evidence[0],snapshot.ip_evidence[0]],
  [snapshot.ip_evidence[0],{...snapshot.ip_evidence[1],trend:'incremental'}],
  [snapshot.ip_evidence[0],{...snapshot.ip_evidence[1],step:1}],
  [snapshot.ip_evidence[0],{...snapshot.ip_evidence[1],ip:'invalid'}]]) {
  assert.equal(validNATPlan({snapshot:{...snapshot,ip_evidence},addresses:[]}),false);
 }
 const evidence={ip:'198.51.100.2',targets:3,ports:3,trend:'decremental',step:2};
 assert.equal(validNATPlan({snapshot:{...snapshot,type:'symmetric',predictability:'decremental',ip_evidence:[snapshot.ip_evidence[0],evidence]},addresses:[]}),true);
 assert.equal(validPunch({kind:'ready',round:1,mode:'pool',plan:{ip:'198.51.100.2',port:40000,target:39980,width:20,sockets:10}}),true);
 // Worst ordinary probe: three different exits for each of eight targets.
 const maximal={...snapshot,samples:24,reason:'测'.repeat(256),ip_evidence:Array.from({length:24},(_,i)=>({ip:`198.51.100.${i+1}`,targets:1,ports:1,trend:'unknown'}))};
 assert.equal(validNATPlan({snapshot:maximal,addresses:[]}),true);
});

test('pool ready requires both offers and survives Worker replay',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 const send=(ws,to,punch)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:to,punch}));
 const ready={kind:'ready',round:1,mode:'pool',plan:{ip:'198.51.100.1',port:40000,target:40001,width:20,sockets:10}};
 const count=()=>b.messages.filter(m=>m.type==='nat_punch').length;
 await send(a,'beta',ready);assert.equal(count(),0);
 const offer={kind:'offer',pool_prediction:true,nonce:'a'.repeat(64),snapshot:{status:'complete',type:'address_pool',predictability:'unverified',samples:2,ip_evidence:[{ip:'198.51.100.1',targets:1,ports:1,trend:'unknown'},{ip:'198.51.100.2',targets:1,ports:1,trend:'unknown'}]},config:{sockets:10,scan_width:20,dual_sockets:32,dual_retry_sockets:64,timeout:'10s',round_timeout:'2.5s'}};
 await send(a,'beta',offer);assert.equal(count(),1);
 await send(a,'beta',ready);assert.equal(count(),1);
 await send(b,'alpha',offer);
 await send(a,'beta',ready);assert.equal(count(),2);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.equal(count(),4);
 assert.deepEqual(b.messages.filter(m=>m.type==='nat_punch').at(-2).punch.snapshot.ip_evidence,offer.snapshot.ip_evidence);
});

const holeCaps={hole_version:HOLE_VERSION,relay_policy:RELAY_POLICY,relay_pairing:'all-pairs-v1',relay_order:[],relay_enabled:false};
test('ICE first switches generation before NAT punching and preserves its retry budget',async()=>{
 for(const rounds of [false,true]) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const caps={punch_version:'nat-punch-v1',direct_order:'ice-first-v1',...(rounds?{relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'}:{})};
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
  let record=[...r.ice.records.values()][0];
  assert.equal(a.messages.filter(m=>m.type==='transport_ready').at(-1).direct_stage,'ice');
  const punch={type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:'beta',punch:{kind:'abort'}};
  await r.webSocketMessage(a,JSON.stringify(punch));
  assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,0);
  const restart={type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',...(rounds?{relay_round:1}:{}),reason:'ice_failed'};
  await r.webSocketMessage(a,JSON.stringify(restart));
  await r.webSocketMessage(b,JSON.stringify(restart));
  assert.equal(record.generation,'2');
  assert.equal(record.direct_stage,'punch');
  assert.equal(record.direct_failures,0);
  assert.equal(record.retry_after_ms,0);
  await r.webSocketMessage(a,JSON.stringify(punch));
  assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,0);
  await r.webSocketMessage(a,JSON.stringify({...punch,transport_generation:record.generation}));
  assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,1);
  const resumed=new Room(f.state,{});
  await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
  record=[...resumed.ice.records.values()][0];
  assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).direct_stage,'punch');
  for(let attempt=1;attempt<=3;attempt++) {
   const failed={...restart,expected_generation:record.generation,reason:'path_failed'};
   await resumed.webSocketMessage(a,JSON.stringify(failed));
   await resumed.webSocketMessage(b,JSON.stringify(failed));
   const ready=a.messages.filter(m=>m.type==='transport_ready').at(-1);
   assert.equal(ready.phase,attempt<3?'direct':'relay_udp');
   assert.equal(ready.direct_stage,attempt<3?'punch':'ice');
  }
  await resumed.webSocketMessage(a,JSON.stringify({...restart,expected_generation:record.generation,phase:'direct',...(rounds?{relay_round:0}:{}),reason:'retry'}));
  assert.equal(record.direct_stage,'ice');
  assert.equal(record.direct_failures,0);
 }
});
test('ICE first falls back directly to relay after unsupported NAT and disables mixed ordering',async()=>{
 for(const compatible of [false,true]) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const caps={punch_version:'nat-punch-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps,direct_order:'ice-first-v1'}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,...(compatible?{direct_order:'ice-first-v1'}:{})}));
  const record=[...r.ice.records.values()][0];
  assert.equal(!!record.punch_version,compatible);
  assert.equal(!!record.direct_order,compatible);
  const restart={type:'transport_restart',transport_id:record.id,relay_round:1};
  if(compatible) {
   await r.webSocketMessage(a,JSON.stringify({...restart,expected_generation:record.generation,reason:'ice_failed'}));
   assert.equal(record.round,0);
   assert.equal(record.direct_stage,'punch');
  }
  await r.webSocketMessage(a,JSON.stringify({...restart,expected_generation:record.generation,reason:compatible?'direct_exhausted':'path_failed'}));
  assert.equal(record.round,1);
 }
 assert.equal(iceJoinFields({...join('alpha',['one']),punch_version:'nat-punch-v1',direct_order:'invalid'}),null);
});
test('ordered punch reservations require negotiated capability and preserve model rounds on replay',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket();let b=f.socket();
 const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,punch_schedule:undefined}));
 let record=[...r.ice.records.values()][0];
 const sendPunch=(ws,to,punch,generation=record.generation)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:generation,to_peer_id:to,punch}));
 const punches=ws=>ws.messages.filter(m=>m.type==='nat_punch');
 assert.equal(record.punch_schedule,'');
 await sendPunch(a,'beta',{kind:'reserve'}); assert.equal(punches(b).length,0);
 const previous=record.generation;
 b=f.socket();
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 record=[...r.ice.records.values()][0];
 assert.notEqual(record.generation,previous);
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).punch_schedule,'ordered-v1');
 const model={kind:'model',round:1,snapshot:{status:'complete',type:'symmetric',mapping_behavior:'endpoint_dependent',predictability:'incremental',samples:9,port_step:1,public_ip:'198.51.100.1',last_mapping_port:40000,allocation_scope:'cross_socket'}};
 await sendPunch(a,'beta',model); assert.equal(punches(b).length,0);
 await sendPunch(b,'alpha',{kind:'reserve'}); assert.equal(punches(a).length,0);
 await sendPunch(b,'alpha',{kind:'reserved'}); assert.equal(punches(a).length,0);
 await sendPunch(a,'beta',{kind:'reserve'},previous); assert.equal(punches(b).length,0);
 await sendPunch(a,'beta',{kind:'reserve'}); assert.equal(punches(b).length,1);
 await sendPunch(b,'alpha',{kind:'reserved'}); assert.equal(punches(a).length,1);
 await sendPunch(a,'beta',{...model,snapshot:{...model.snapshot,port_step:-1}}); assert.equal(punches(b).length,1);
 for(let round=1;round<=3;round++) {
  await sendPunch(a,'beta',{...model,round});
  await sendPunch(a,'beta',{kind:'ready',round,mode:'fixed',plan:{ip:'198.51.100.1',port:40000,target:40016,width:1,sockets:32}});
  await sendPunch(b,'alpha',{kind:'ready',round,mode:'fixed',plan:{ip:'198.51.100.2',port:50000,target:50016,width:1,sockets:32}});
  await sendPunch(a,'beta',{kind:'go',round});
  await sendPunch(a,'beta',{kind:'failed',round});
 }
 assert.equal(punches(b).length,13);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.equal(punches(b).length,26);
 assert.deepEqual(punches(b).slice(13).map(m=>m.punch),punches(b).slice(0,13).map(m=>m.punch));
});
test('calibration capability requires ordered ICE first peers and resets generation',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket();let b=f.socket();
 const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',direct_order:'ice-first-v1',punch_calibration:'barrier-v1'};
 for(const invalid of [{punch_calibration:'unknown'},{punch_schedule:undefined},{direct_order:undefined},{punch_version:undefined}]) {
  assert.equal(iceJoinFields({...join('alpha',['one']),...caps,...invalid}),null);
 }
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,punch_calibration:undefined}));
 let record=[...r.ice.records.values()][0];
 assert.equal(record.punch_calibration,'');
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).punch_calibration,undefined);
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',reason:'ice_failed'}));
 assert.equal(record.direct_stage,'punch');
 const send=punch=>r.webSocketMessage(a,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:'beta',punch}));
 await send({kind:'reserve'});
 const before=b.messages.filter(m=>m.type==='nat_punch').length;
 await send({kind:'calibrate',round:1,trial:1});
 assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,before);
 const generation=record.generation;
 b=f.socket();
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 record=[...r.ice.records.values()][0];
 assert.notEqual(record.generation,generation);
 assert.equal(record.direct_stage,'ice');
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).punch_calibration,'barrier-v1');
});

for (const modelReuse of [false,true]) test(`calibration barriers gate bilateral retries and GO and survive complete replay (model reuse: ${modelReuse})`,async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',direct_order:'ice-first-v1',punch_calibration:'barrier-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',reason:'ice_failed'}));
 assert.equal(record.direct_stage,'punch');
 const send=(ws,to,punch)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:to,punch}));
 const punches=ws=>ws.messages.filter(m=>m.type==='nat_punch').map(m=>m.punch);
 const rejected=async(ws,to,punch)=>{
  const receiver=ws===a?b:a,before=punches(receiver).length;
  await send(ws,to,punch);
  assert.equal(punches(receiver).length,before,JSON.stringify(punch));
  if(punch.kind==='calibrated') {
   const error=ws.messages.at(-1);
   assert.equal(error.code,'punch_calibration_rejected');
   assert.match(error.message,/结果未转发/);
   assert.equal(error.transport_generation,record.generation);
   assert.deepEqual(error.punch,{kind:'calibrated',round:punch.round,trial:punch.trial});
  }
 };
 const snapshot={status:'complete',type:'symmetric',mapping_behavior:'endpoint_dependent',predictability:'incremental',samples:9,port_step:1,public_ip:'198.51.100.1',last_mapping_port:40000,allocation_scope:'cross_socket'};
 const plan={ip:'198.51.100.1',port:40001,target:40017,width:1,sockets:32};
 const offer={kind:'offer',nonce:'a'.repeat(64),snapshot,model_reuse:modelReuse,config:{sockets:10,scan_width:20,dual_sockets:32,dual_retry_sockets:64,timeout:'10s',round_timeout:'2.5s'}};
 await send(a,'beta',offer);await send(b,'alpha',offer);
 await rejected(a,'beta',{kind:'calibrate',round:1,trial:1});
 await send(a,'beta',{kind:'reserve'});await send(b,'alpha',{kind:'reserved'});
 for(let round=1;round<=3;round++) {
  if (modelReuse) {
   await rejected(a,'beta',{kind:'model',round,snapshot});
  } else {
   await send(a,'beta',{kind:'model',round,snapshot});
   await rejected(a,'beta',{kind:'calibrate',round,trial:1});
   await send(b,'alpha',{kind:'model',round,snapshot});
  }
  await rejected(a,'beta',{kind:'ready',round,mode:'fixed',plan});
  for(let trial=1;trial<=3;trial++) {
   const start={kind:'calibrate',round,trial},ok={kind:'calibrated',round,trial,result:'ok',mode:'fixed',plan};
   await rejected(a,'beta',{...start,trial:trial+1});
   await send(a,'beta',start);
   await rejected(a,'beta',ok);
   await send(b,'alpha',start);
   await send(a,'beta',trial===3?ok:{...ok,result:'retry',plan:undefined});
   await rejected(a,'beta',{kind:'go',round,trial});
   await send(b,'alpha',ok);
   if(trial<3) await rejected(a,'beta',{kind:'go',round,trial});
  }
  await rejected(a,'beta',{kind:'go',round,trial:1});
  await rejected(b,'alpha',{kind:'go',round,trial:3});
  await send(a,'beta',{kind:'go',round,trial:3});
  await send(a,'beta',{kind:'failed',round});await send(b,'alpha',{kind:'failed',round});
 }
 await send(a,'beta',{kind:'abort'});await send(b,'alpha',{kind:'abort'});
 const sent=punches(b);
 assert.equal(sent.length,modelReuse?27:30,'all three rounds and trials fit in bounded cache');
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.deepEqual(punches(b).slice(sent.length),sent);
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).punch_calibration,'barrier-v1');
});

for (const modelReuse of [false,true]) test(`retained cone pool negotiates, gates extra rounds and preserves plans through retries and replay (model reuse: ${modelReuse})`,async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',direct_order:'ice-first-v1',punch_calibration:'barrier-v1',punch_cone_pool:'retained-v1'};
 assert.equal(iceJoinFields({...join('alpha'),...caps,punch_calibration:undefined}),null);
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 assert.equal(a.messages.filter(m=>m.type==='transport_ready').at(-1).punch_cone_pool,'retained-v1');
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',reason:'ice_failed'}));
 const send=(ws,to,punch)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:to,punch}));
 const rejected=async(ws,to,punch)=>{
  const receiver=ws===a?b:a,before=receiver.messages.filter(m=>m.type==='nat_punch').length;
  await send(ws,to,punch);
  assert.equal(receiver.messages.filter(m=>m.type==='nat_punch').length,before,JSON.stringify(punch));
 };
 const cone={status:'complete',type:'port_restricted_cone',mapping_behavior:'endpoint_independent',predictability:'stable',no_pat:'suspected',port_preservation:'preserved',samples:8,
  ip_evidence:[1,2,3,4].map(n=>({ip:`198.51.100.${n}`,targets:2,ports:1,trend:'stable'}))};
 const symmetric={status:'complete',type:'symmetric',mapping_behavior:'endpoint_dependent',predictability:'incremental',samples:3};
 const original={ip:'198.51.100.1',port:40000,target:40000,width:1,sockets:1};
 const remote={ip:'203.0.113.1',port:50000,target:50001,width:20,sockets:10};
 const selected={kind:'pool_select',round:4,mode:'single',plan:{...original,ip:'198.51.100.2'}};
 if (modelReuse) {
  const offer={kind:'offer',nonce:'a'.repeat(64),model_reuse:true,config:{sockets:10,scan_width:20,dual_sockets:32,dual_retry_sockets:64,timeout:'10s',round_timeout:'2.5s'}};
  await send(a,'beta',{...offer,snapshot:cone});await send(b,'alpha',{...offer,snapshot:symmetric});
 }
 await rejected(a,'beta',selected);
 await send(a,'beta',{kind:'reserve'});await send(b,'alpha',{kind:'reserved'});
 for(let round=1;round<=6;round++) {
  let plan=original;
  if(round<=3) {
   if (!modelReuse) {
    await send(a,'beta',{kind:'model',round,snapshot:cone});
    await send(b,'alpha',{kind:'model',round,snapshot:symmetric});
   }
  } else {
   plan={...original,ip:`198.51.100.${round-2}`};
   const selection={...selected,round,plan:Object.fromEntries(Object.entries(plan).reverse())};
   await rejected(b,'alpha',selection);
   await rejected(a,'beta',{...selection,plan:{...plan,ip:original.ip}});
   await rejected(a,'beta',{...selection,plan:{...plan,ip:'198.51.100.99'}});
   await rejected(a,'beta',{...selection,plan:{...plan,port:40001,target:40001}});
   if(round>4) await rejected(a,'beta',{...selection,plan:{...plan,ip:'198.51.100.2'}});
   await rejected(a,'beta',{kind:'calibrate',round,trial:1});
   await send(a,'beta',selection);
   assert.deepEqual(b.messages.filter(m=>m.type==='nat_punch').at(-1).punch,selection);
  }
  const trials=round===4?2:1;
  for(let trial=1;trial<=trials;trial++) {
   for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'calibrate',round,trial});
   const result={kind:'calibrated',round,trial,result:'ok',mode:'single',plan};
   if(round>3) await rejected(a,'beta',{...result,plan:{...plan,ip:original.ip}});
   await send(a,'beta',result);
   await send(b,'alpha',{...result,plan:trial===trials?remote:undefined,result:trial===trials?'ok':'retry'});
  }
  await send(a,'beta',{kind:'go',round,trial:trials});
  assert.equal(b.messages.filter(m=>m.type==='nat_punch').at(-1).punch.kind,'go');
  for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'failed',round,result:'unreachable'});
 }
 assert.equal(validPunch({...selected,round:7}),false);
 const before=b.messages.filter(m=>m.type==='nat_punch').length;
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,2*before);
 const generation=record.generation;
 const replacement=f.socket();
 await resumed.webSocketMessage(replacement,JSON.stringify({...join('beta',[],['one']),...caps,punch_cone_pool:undefined}));
 const latest=[...resumed.ice.records.values()][0];
 assert.notEqual(latest.generation,generation);
 assert.equal(latest.punch_cone_pool,'');
 await resumed.webSocketMessage(a,JSON.stringify({type:'nat_punch',transport_id:latest.id,transport_generation:latest.generation,to_peer_id:'beta',punch:selected}));
 assert.equal(replacement.messages.some(m=>m.type==='nat_punch'),false);
});

test('reused models refresh only after bilateral failure and require both new models before calibration',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',direct_order:'ice-first-v1',punch_calibration:'barrier-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 assert.equal(a.messages.filter(m=>m.type==='transport_ready').at(-1).punch_model_reuse,true);
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',reason:'ice_failed'}));
 const send=(ws,to,punch)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:to,punch}));
 const punches=ws=>ws.messages.filter(m=>m.type==='nat_punch').map(m=>m.punch);
 const rejected=async(ws,to,punch)=>{
  const receiver=ws===a?b:a,before=punches(receiver).length;
  await send(ws,to,punch);assert.equal(punches(receiver).length,before,JSON.stringify(punch));
 };
 const snapshot={status:'complete',type:'symmetric',mapping_behavior:'endpoint_dependent',predictability:'incremental',samples:9,port_step:1,public_ip:'198.51.100.1',last_mapping_port:40000,allocation_scope:'cross_socket'};
 const offer={kind:'offer',nonce:'a'.repeat(64),snapshot,model_reuse:true,config:{sockets:10,scan_width:20,dual_sockets:32,dual_retry_sockets:64,timeout:'10s',round_timeout:'2.5s'}};
 assert.equal(validPunch({...offer,model_reuse:'true'}),false);
 await send(a,'beta',offer);
 await send(a,'beta',{kind:'reserve'});await send(b,'alpha',{kind:'reserved'});
 await rejected(a,'beta',{kind:'calibrate',round:1,trial:1}); // Missing peer offer.
 await send(b,'alpha',offer);
 for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'calibrate',round:1,trial:1});
 await send(a,'beta',{kind:'failed',round:1});
 await rejected(a,'beta',{kind:'calibrate',round:2,trial:1}); // Peer is still running.
 await send(b,'alpha',{kind:'failed',round:1,result:'refresh'});
 await rejected(a,'beta',{kind:'calibrate',round:2,trial:1});
 const refreshed={...snapshot,port_step:2,last_mapping_port:41000};
 await send(a,'beta',{kind:'model',round:2,snapshot:refreshed});
 await rejected(a,'beta',{kind:'calibrate',round:2,trial:1});
 await send(b,'alpha',{kind:'model',round:2,snapshot:refreshed});
 for(const [ws,to] of [[a,'beta'],[b,'alpha']]) {
  await send(ws,to,{kind:'calibrate',round:2,trial:1});
  assert.deepEqual(punches(ws===a?b:a).at(-1),{kind:'calibrate',round:2,trial:1});
 }
 for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'failed',round:2});
 await rejected(a,'beta',{kind:'model',round:3,snapshot});
 await send(a,'beta',{kind:'calibrate',round:3,trial:1});
 assert.deepEqual(punches(b).at(-1),{kind:'calibrate',round:3,trial:1});
 const sent=punches(b),resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.deepEqual(punches(b).slice(sent.length),sent);
});

test('calibration results validate plans and stop retries after terminal outcomes',async()=>{
 const plan={ip:'198.51.100.1',port:40001,target:40002,width:1,sockets:1};
 const result={kind:'calibrated',round:1,trial:1,mode:'next',result:'ok',plan};
 for(const invalid of [{trial:0},{trial:4},{trial:undefined},{result:'unknown'},{plan:undefined},{result:'retry'},{plan:{...plan,target:65536}}]) {
  assert.equal(validPunch({...result,...invalid}),false);
 }
 assert.equal(validPunch(result),true);
 for(const outcome of ['refresh','exhausted','ok','abort']) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const caps={punch_version:'nat-punch-v1',punch_schedule:'ordered-v1',direct_order:'ice-first-v1',punch_calibration:'barrier-v1'};
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
  const record=[...r.ice.records.values()][0];
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp',reason:'ice_failed'}));
  assert.equal(record.direct_stage,'punch');
  const send=(ws,to,punch)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:record.generation,to_peer_id:to,punch}));
  await send(a,'beta',{kind:'reserve'});await send(b,'alpha',{kind:'reserved'});
  for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'model',round:1,snapshot:{status:'complete',type:'symmetric',predictability:'incremental',samples:3}});
  for(const [ws,to] of [[a,'beta'],[b,'alpha']]) await send(ws,to,{kind:'calibrate',round:1,trial:1});
  const successful=outcome==='ok' || outcome==='abort';
  await send(a,'beta',{...result,result:successful?'ok':outcome,plan:successful?plan:undefined});
  await send(b,'alpha',result);
  if(outcome==='abort') await send(b,'alpha',{kind:'abort'});
  const before=b.messages.filter(m=>m.type==='nat_punch').length;
  await send(a,'beta',{kind:'calibrate',round:1,trial:2});
  if(outcome!=='ok') await send(a,'beta',{kind:'go',round:1,trial:1});
  assert.equal(b.messages.filter(m=>m.type==='nat_punch').length,before);
 }
});

test('NAT punching negotiates capabilities, gates GO and rejects stale rounds and policies',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 assert.equal(a.messages.filter(m=>m.type==='transport_ready').at(-1).punch_version,'nat-punch-v1');
 const sendPunch=(ws,to,punch,generation=record.generation)=>r.webSocketMessage(ws,JSON.stringify({type:'nat_punch',transport_id:record.id,transport_generation:generation,to_peer_id:to,punch}));
 const count=()=>b.messages.filter(m=>m.type==='nat_punch').length;
 await sendPunch(a,'beta',{kind:'go',round:1}); assert.equal(count(),0);
 const ready={kind:'ready',round:1,mode:'fixed',plan:{ip:'198.51.100.1',port:40000,target:40016,width:1,sockets:32}};
 await sendPunch(a,'beta',{...ready,round:4}); assert.equal(count(),0);
 await sendPunch(a,'beta',{...ready,plan:{...ready.plan,target:65536}}); assert.equal(count(),0);
 await sendPunch(a,'beta',ready,'0'); assert.equal(count(),0);
 const lease=record.lease_until;record.lease_until=0;
 await sendPunch(a,'beta',ready);assert.equal(count(),0);record.lease_until=lease;
 await sendPunch(a,'beta',ready);assert.equal(count(),1);
 await sendPunch(b,'alpha',ready);
 await sendPunch(a,'beta',{kind:'go',round:1});assert.equal(count(),2);
 await sendPunch(a,'beta',{kind:'go',round:1});assert.equal(count(),2);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));assert.equal(count(),4);
});
test('NAT punching retries direct three times then enters relay without double-counting both peers',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={punch_version:'nat-punch-v1',relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 for(let attempt=1;attempt<=3;attempt++) {
  const expected=record.generation;
  const restart={type:'transport_restart',transport_id:record.id,expected_generation:expected,relay_round:1,reason:'path_failed'};
  await r.webSocketMessage(a,JSON.stringify(restart));await r.webSocketMessage(b,JSON.stringify(restart));
  assert.equal(BigInt(record.generation),BigInt(expected)+1n);
  assert.equal(record.round,attempt<3?0:1);
 }
});
test('prediction exhausted skips another direct retry and older peers keep ordinary ICE',async()=>{
 for(const enabled of [true,false]) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const caps={relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps,punch_version:'nat-punch-v1'}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,...(enabled?{punch_version:'nat-punch-v1'}:{})}));
  const record=[...r.ice.records.values()][0];
  assert.equal(!!record.punch_version,enabled);
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:1,reason:'direct_exhausted'}));
  assert.equal(record.round,1);
 }
});
test('NAT status survives relay fallback and validates lease, runtime, epoch and policy',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={relay_policy:RELAY_POLICY,relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:1,phase:'relay_udp'}));
 assert.equal(a.messages.filter(m=>m.type==='transport_ready').at(-1).phase,'relay_udp');
 const nat={snapshot:{status:'complete',type:'endpoint_independent',predictability:'stable',samples:6},addresses:[]};
 const status={type:'nat_status',transport_id:record.id,transport_generation:record.generation,to_peer_id:'beta',nat};
 const count=()=>b.messages.filter(m=>m.type==='nat_status').length;
 for (const field of ['lease_until','runtime','epochs','relay_required']) {
   const saved=record[field];
   record[field]=field==='lease_until'?0:field==='runtime'?['stale','stale']:field==='epochs'?['0','0']:true;
   await r.webSocketMessage(a,JSON.stringify(status));
   assert.equal(count(),0,field);
   record[field]=saved;
 }
 await r.webSocketMessage(a,JSON.stringify({...status,nat:{...nat,snapshot:{...nat.snapshot,reason:'x'.repeat(4097)}}}));
 assert.equal(count(),0);
 await r.webSocketMessage(a,JSON.stringify(status));
 assert.equal(count(),1);assert.deepEqual(b.messages.at(-1).nat,nat);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.equal(count(),2);assert.deepEqual(b.messages.at(-1).nat,nat);
});
test('NAT status is replayed and renewed per generation',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify(join('alpha',['one'])));
 await r.webSocketMessage(b,JSON.stringify(join('beta',[],['one'])));
 const record=[...r.ice.records.values()][0];
 const nat={snapshot:{status:'complete',type:'firewall',predictability:'stable',mapping_behavior:'endpoint_independent',filtering_behavior:'address_dependent',port_preservation:'preserved',no_pat:'not_applicable',samples:6},addresses:[]};
 const status={type:'nat_status',transport_id:record.id,transport_generation:record.generation,to_peer_id:'beta',nat};
 await r.webSocketMessage(a,JSON.stringify(status));
 assert.deepEqual(b.messages.at(-1).nat,nat);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(b,JSON.stringify({type:'transport_sync'}));
 assert.deepEqual(b.messages.at(-1).nat,nat);
 await r.webSocketMessage(a,JSON.stringify({...status,nat:{...nat,snapshot:{...nat.snapshot,type:'symmetric',predictability:'predictable_incremental'}}}));
 assert.equal(b.messages.at(-1).nat.snapshot.type,'firewall');
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'direct'}));
 const count=b.messages.filter(m=>m.type==='nat_status').length;
 await r.webSocketMessage(a,JSON.stringify(status));
 assert.equal(b.messages.filter(m=>m.type==='nat_status').length,count);
 const next={...status,transport_generation:record.generation,nat:{...nat,snapshot:{...nat.snapshot,type:'symmetric',predictability:'predictable_decremental'}}};
 await r.webSocketMessage(a,JSON.stringify(next));
 assert.deepEqual(b.messages.at(-1).nat,next.nat);
});
async function holeFixture({two=false,offline=false,only=true}={}) {
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket(),relay=f.socket();
 const jr={...join('relay-node'),...holeCaps,transport_profiles:[ICE_PROFILE,FIXED_PROFILE],fixed_endpoint:'relay.example:40000',hole_serve:{enabled:true,max_pairs:1,bandwidth_mbps:0}};
 if(!offline)await r.webSocketMessage(relay,JSON.stringify(jr));
 const ja={...join('alpha',['one']),...holeCaps,hole_device:'relay-node',relay_order:['hole','udp'],relay_enabled:true,relay_only:only};
 const jb={...join('beta',[],['one']),...holeCaps,relay_only:only,...(two?{hole_device:'relay-node',relay_order:['hole']}: {})};
 await r.webSocketMessage(a,JSON.stringify(ja));await r.webSocketMessage(b,JSON.stringify(jb));
 return {...f,r,a,b,relay,jr,ja,jb,record:[...r.ice.records.values()][0]};
}
test('Hole shared plans collapse relay pairs, sort distinct names, and keep TURN order',()=>{
 assert.deepEqual(deviceRelayPlan(['hole','udp'],['hole','tls'],'z','a'),[{device:'a',a:'hole',b:'hole'},{device:'z',a:'hole',b:'hole'},{a:'relay_udp',b:'relay_tls'}]);
 assert.deepEqual(deviceRelayPlan(['hole','udp'],['hole','tls'],'same','same'),[{device:'same',a:'hole',b:'hole'},{a:'relay_udp',b:'relay_tls'}]);
});
test('Hole authorization needs only a real endpoint relation and both sides support it',async()=>{
 const f=await holeFixture();const h=f.record.hole;
 assert.equal(f.record.round,1);assert.equal(h.device,'relay-node');assert.match(h.route,/^[a-f0-9]{32}$/);
 assert.equal(f.relay.messages.filter(m=>m.type==='hole_grant').length,1);
 for(const s of [f.a,f.b]) {const ready=s.messages.filter(m=>m.type==='transport_ready').at(-1);assert.equal(ready.phase,'hole');assert.equal(ready.hole.route,h.route);}
 assert.equal(f.r.ice.records.size,1);
 const old=f.socket();await f.r.webSocketMessage(old,JSON.stringify(join('beta',[],['one'])));
 assert.ok(f.a.messages.some(m=>m.code==='peer_upgrade_required'));
 assert.ok(old.messages.some(m=>m.code==='peer_upgrade_required'));
 assert.equal([...f.r.ice.records.values()].filter(r=>r.online).length,0);
});
test('Hole direct-first, offline fallback, lease renewal, hibernation and revocation',async()=>{
 const f=await holeFixture({only:false});assert.equal(f.record.round,0);assert.equal(f.record.hole,undefined);
 await f.r.webSocketMessage(f.a,JSON.stringify({type:'transport_restart',transport_id:f.record.id,expected_generation:f.record.generation,relay_round:1}));
 const route=f.record.hole.route,gen=f.record.generation;
 const resumed=new Room(f.state,{});await resumed.webSocketMessage(f.a,JSON.stringify({type:'transport_renew'}));
 assert.equal([...resumed.ice.records.values()][0].hole.route,route);
 assert.equal(f.relay.messages.filter(m=>m.type==='hole_grant').at(-1).transport_generation,gen);
 const live=[...resumed.ice.records.values()][0];live.lease_until=Date.now()-1;
 await resumed.webSocketMessage(f.a,JSON.stringify({type:'transport_renew'}));
 assert.notEqual(live.hole.route,route);assert.ok(BigInt(live.generation)>BigInt(gen));
 await resumed.webSocketClose(f.b,1000,'closed',true);
 assert.ok(f.relay.messages.some(m=>m.type==='hole_revoke'));assert.equal(live.hole,null);
 const missing=await holeFixture({offline:true});assert.match(missing.record.hole.error,/暂未在线/);
});
test('Hole service capacity is per pair and relay restart invalidates grants',async()=>{
 const f=await holeFixture();const old=f.record.hole.route;
 const c=f.socket();await f.r.webSocketMessage(c,JSON.stringify({...join('charlie',[],['one']),...holeCaps,relay_only:true}));
 const other=[...f.r.ice.records.values()].find(r=>r.b==='charlie');assert.match(other.hole.error,/容量/);
 const replacement=f.socket();await f.r.webSocketMessage(replacement,JSON.stringify({...f.jr,runtime_id:'c'.repeat(32),fixed_endpoint:'new.example:40001'}));
 assert.notEqual(f.record.hole.route,old);assert.equal(f.record.hole.endpoint,'new.example:40001');
 await f.r.webSocketClose(replacement,1000,'closed',true);assert.match(f.record.hole.error,/暂未在线/);
});
Object.defineProperty(globalThis,'crypto',{value:webcrypto});
test('Hole relay socket reconnect rotates generation even with unchanged identity',async()=>{
 const f=await holeFixture();const gen=f.record.generation,route=f.record.hole.route;
 const replacement=f.socket();await f.r.webSocketMessage(replacement,JSON.stringify(f.jr));
 assert.ok(BigInt(f.record.generation)>BigInt(gen));assert.notEqual(f.record.hole.route,route);
 assert.equal(f.relay.closed,true,'replaced relay socket remained authorized');
 const live=new Room(f.state,{});
 await live.webSocketMessage(replacement,JSON.stringify({type:'transport_sync'}));
 const record=[...live.ice.records.values()][0];
 assert.equal(record.generation,f.record.generation);
 record.lease_until=Date.now()-1;
 const before=replacement.messages.filter(m=>m.type==='hole_grant').length;
 await live.webSocketMessage(replacement,JSON.stringify({type:'transport_sync'}));
 assert.equal(replacement.messages.filter(m=>m.type==='hole_grant').length,before,'relay sync renewed an expired authorization');
});
test('Hole install failure persists across Worker hibernation',async()=>{
 const f=await holeFixture();
 await f.r.webSocketMessage(f.relay,JSON.stringify({type:'hole_failed',transport_id:f.record.id,transport_generation:f.record.generation}));
 const live=new Room(f.state,{});
 await live.webSocketMessage(f.a,JSON.stringify({type:'transport_sync'}));
 assert.match([...live.ice.records.values()][0].hole.error,/不可用/);
});
test('concurrent room initialization preserves one key and the latest mirror',async()=>{
 const f=fixture(),r=new Room(f.state,{});
 await Promise.all([r.loadState(),r.loadState()]);
 await r.ice.setRoomName('fixture');
 const keys=await Promise.all(Array.from({length:8},async()=>{await r.control.ensureKey();return r.control.publicKey}));
 assert.equal(new Set(keys).size,1);
 assert.equal(f.values.get('room_control_key').public,keys[0]);
 const a=f.socket(),b=f.socket();
 r.registered.set('alpha',{member:join('alpha'),ws:a});r.control.rebuild();
 let started,release;
 const waiting=new Promise(resolve=>{started=resolve}),gate=new Promise(resolve=>{release=resolve});
 const put=f.state.storage.put;let first=true;
 f.state.storage.put=async(k,v)=>{if(k==='room_mirror'&&first){first=false;started();await gate}return put(k,v)};
 const one=r.control.publishSnapshot();await waiting;
 r.registered.set('beta',{member:join('beta'),ws:b});r.control.rebuild();
 const two=r.control.publishSnapshot();release();await Promise.all([one,two]);
 const mirror=f.values.get('room_mirror');
 assert.deepEqual(mirror.members.map(m=>m.device_name),['alpha','beta']);
 assert.equal(mirror.state_revision,String(r.control.revision));
 assert.equal(mirror.worker_version,'1.1.0');
});

test('concurrent initial joins recheck the room token after asynchronous authentication',async t=>{
 for(const pauseAt of ['certificate','ensureKey']) {
  for(const sameToken of [false,true]) {
   await t.test(`${pauseAt}: ${sameToken?'matching':'different'} tokens`,async()=>{
    const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
    await r.loadState();await r.ice.setRoomName('fixture');
    let started,release,waiting=0;
    const bothWaiting=new Promise(resolve=>{started=resolve}),gate=new Promise(resolve=>{release=resolve});
    const pause=async()=>{if(++waiting===2)started();await gate};
    const ensureKey=r.control.ensureKey.bind(r.control);
    r.control.certificate=async()=>{if(pauseAt==='certificate')await pause()};
    r.control.ensureKey=async()=>{if(pauseAt==='ensureKey')await pause();await ensureKey()};
    const modern=name=>({...join(name),core_version:'1.1.0',certificate:`fixture-${name}`});
    const first=r.webSocketMessage(a,JSON.stringify(modern('alpha')));
    const second=r.webSocketMessage(b,JSON.stringify({...modern('beta'),token:sameToken?'room-secret':'other-secret'}));
    await bothWaiting;release();await Promise.all([first,second]);
    assert.equal(a.messages.filter(m=>m.type==='joined').length,1);
    assert.equal(b.messages.some(m=>m.type==='joined'),sameToken);
    assert.equal(r.roomToken,'room-secret');assert.equal(f.values.get('token'),'room-secret');
    assert.equal(r.registered.has('beta'),sameToken);
    assert.equal(f.values.has('member:beta'),sameToken);
    if(!sameToken) {
     assert.equal(b.messages.find(m=>m.type==='error').code,'invalid_token');
     assert.equal(b.closed,true);assert.equal(b.attachment,null);
    }
   });
  }
 }
});

test('a socket cannot join again while its first membership write is pending',async()=>{
 const f=fixture(),r=new Room(f.state,{}),ws=f.socket();
 await r.loadState();
 let started,release;
 const writing=new Promise(resolve=>{started=resolve}),gate=new Promise(resolve=>{release=resolve});
 const put=f.state.storage.put;
 f.state.storage.put=async(k,v)=>{if(k==='member:alpha'){started();await gate}return put(k,v)};
 const first=r.webSocketMessage(ws,JSON.stringify(join('alpha')));
 await writing;
 try {
  assert.equal(ws.attachment,null);
  await r.webSocketMessage(ws,JSON.stringify(join('beta')));
 } finally {
  release();await first;
 }
 assert.equal(ws.messages.some(m=>m.code==='already_joined'),true);
 assert.equal(ws.messages.filter(m=>m.type==='joined').length,1);
 assert.deepEqual([...r.registered.keys()],['alpha']);
 assert.equal(f.values.has('member:beta'),false);
});

test('stale room graphs cannot abort current updates or restore old network epochs',async()=>{
 const f=fixture(),r=new Room(f.state,{});
 await r.ice.setRoomName('fixture');
 const keys=await webcrypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},false,['sign','verify']);
 const members=['alpha','beta'].map(name=>({...join(name),core_version:'1.1.0',certificate:'fixture-'+name}));
 for(const member of members) {
  r.registered.set(member.device_name,{member,ws:f.socket()});
  r.control.keys.set(member.runtime_id+'/'+member.cert_fingerprint,{verify:keys.publicKey});
 }
 const signed=async(member,revision,epoch)=>{
  const node={device_name:member.device_name,runtime_id:member.runtime_id,core_version:member.core_version,worker_version:'1.1.0',
   revision,transport_epoch:epoch,signal_online:true,resident:true,neighbours:[]};
  const frame={id:revision.padStart(32,'0'),room:'fixture',origin:member.device_name,runtime:member.runtime_id,kind:'node',
   body:Buffer.from(JSON.stringify(node)).toString('base64'),certificate:member.certificate,hops:16};
  frame.signature=Buffer.from(await webcrypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},keys.privateKey,roomSigningBytes(frame))).toString('base64');
  return frame;
 };
 const [alpha,beta]=members,old=await signed(alpha,'1','1');
 assert.equal(await r.control.update([old]),true);
 alpha.transport_epoch='2';
 const current=await signed(alpha,'2','2');
 assert.equal(await r.control.update([old,current]),true);
 assert.equal(r.control.nodes.get('alpha').node.transport_epoch,'2');
 await assert.rejects(r.control.update([{...current,signature:Buffer.alloc(64).toString('base64')}]),/signature/);
 assert.equal(r.control.nodes.get('alpha').frame,current);
 const pending=await signed(alpha,'3','2'),other=await signed(beta,'1','1');
 const certificate=r.control.certificate.bind(r.control);
 r.control.certificate=async member=>{
  if(member.device_name==='beta') alpha.transport_epoch='3';
  return certificate(member);
 };
 assert.equal(await r.control.update([pending,other]),true);
 assert.equal(r.control.nodes.get('alpha').node.revision,'2');
 assert.equal(r.control.nodes.get('beta').frame,other);
 const fresh=await signed(alpha,'4','3');
 assert.equal(await r.control.update([fresh,old]),true);
 assert.equal(r.control.nodes.get('alpha').node.transport_epoch,'3');
 const staleIdentity={...other,runtime:'c'.repeat(32)};
 assert.equal(await r.control.update([staleIdentity]),false);
 assert.equal(r.control.nodes.get('beta').frame,other);
 assert.equal(f.values.get('room_nodes').find(frame=>frame.origin==='alpha').id,fresh.id);
});

function fixture(){
 const values=new Map(),sockets=[];
 let alarm=null;const counts={puts:0,lists:0,alarms:0};
 const storage={async get(k){return structuredClone(values.get(k))},async put(k,v){counts.puts++;values.set(k,structuredClone(v))},async delete(keys){for(const k of Array.isArray(keys)?keys:[keys])values.delete(k)},async list({prefix='' }={}){counts.lists++;return new Map([...values].filter(([k])=>k.startsWith(prefix)).map(([k,v])=>[k,structuredClone(v)]))},async transaction(fn){return fn(storage)},async setAlarm(at){counts.alarms++;alarm=at},async getAlarm(){return alarm},async deleteAlarm(){alarm=null}};
 const state={storage,id:{toString:()=> 'fixture-room'},getWebSockets:()=>sockets.filter(s=>!s.closed)};
 function socket(){const s={attachment:null,closed:false,messages:[],serializeAttachment(v){this.attachment=structuredClone(v)},deserializeAttachment(){return structuredClone(this.attachment)},send(v){this.messages.push(JSON.parse(v))},close(){this.closed=true}};sockets.push(s);return s}
 return{values,state,socket,counts};
}

function panelFixture() {
 Object.defineProperty(webcrypto.subtle, 'timingSafeEqual', { value: timingSafeEqual, configurable: true });
 const brokerState=fixture(), roomState=fixture();
 let broker,room,touches=0;
 const env={PASSWORD:'信令 password',ROOMS:{idFromName:name=>name,get(name){if(name===PANEL_SESSION_NAME)return broker;touches++;return room;}}};
 broker=new Room(brokerState.state,env);room=new Room(roomState.state,env);
 const request=(action,body={},cookie='',origin='https://example.test',extra={})=>new Request('https://example.test/panel/'+action,{
  method:action.startsWith('live')?'GET':'POST',headers:{Origin:origin,Cookie:cookie,...(action.startsWith('live')?{Upgrade:'websocket'}:{}),...extra},
  ...(action.startsWith('live')?{}:{body:JSON.stringify(body)})});
 const call=(...args)=>worker.fetch(request(...args),env);
 return {env,call,request,brokerState,roomState,get touches(){return touches},get room(){return room},get broker(){return broker},
  hibernate(){broker=new Room(brokerState.state,env);room=new Room(roomState.state,env)}};
}

test('panel sessions use protected cookies and require HTTPS, same origin and both passwords', async () => {
 const f=panelFixture();
 assert.equal(panelPath(undefined),'/panel');
 assert.equal(panelPath('/custom/view'),'/custom/view');
 assert.equal(panelPath('/ws'),null);
 assert.equal(panelPath('off'),null);
 assert.equal((await f.call('auth',{password:f.env.PASSWORD},'','https://evil.test')).status,403);
 assert.equal((await worker.fetch(new Request('http://example.test/panel/auth',{method:'POST',headers:{Origin:'http://example.test'},body:JSON.stringify({password:f.env.PASSWORD})}),f.env)).status,403);
 assert.equal((await f.call('auth',{password:'bad'})).status,401);
 assert.equal((await f.call('auth',null)).status,400);
 assert.equal((await f.call('auth',{password:'x'.repeat(8192)})).status,413);
 assert.equal((await f.call('room',{room:'demo',token:'room-secret'})).status,401);
 assert.equal((await f.call('live',{},'',undefined,{'X-Hole-Panel-Proof':'forged'})).status,401);
 const reserved=new Request('https://example.test/ws?room='+encodeURIComponent(PANEL_SESSION_NAME),{headers:{Authorization:'Bearer password',Upgrade:'websocket'}});
 assert.equal((await worker.fetch(reserved,{...f.env,PASSWORD:'password'})).status,400);
 assert.equal(f.touches,0);
 const response=await f.call('auth',{password:f.env.PASSWORD});
 assert.equal(response.status,200);
 const setCookie=response.headers.get('Set-Cookie'),cookie=setCookie.split(';')[0];
 assert.match(setCookie,/^__Host-hole-panel=[a-f0-9]{64}; Path=\/; Secure; HttpOnly; SameSite=Strict$/);
 assert.equal(response.headers.get('Cache-Control'),'no-store');
 assert.deepEqual(Object.keys(await response.json()).sort(),['expires','room']);
 assert.equal((await f.call('live',{},cookie)).status,409);
 assert.equal((await f.call('room',{room:PANEL_SESSION_NAME,token:'room-secret'},cookie)).status,400);
 assert.equal(f.touches,0);
 assert.equal((await f.call('room',{room:'demo',token:'room-secret'},cookie)).status,403);
 assert.equal(f.roomState.values.has('token'),false);
 await f.room.webSocketMessage(f.roomState.socket(),JSON.stringify(join('alpha')));
 assert.equal((await f.call('room',{room:'demo',token:'wrong'},cookie)).status,403);
 assert.equal((await f.call('room',{room:'demo',token:'room-secret'},cookie)).status,200);
 f.hibernate();
 const restored=await f.call('session',{},cookie);
 assert.equal((await restored.json()).room,'demo');
 assert.equal(restored.headers.get('Set-Cookie'),null);
 const stored=JSON.stringify([...f.brokerState.values]);
 for(const secret of [f.env.PASSWORD,'room-secret',cookie.split('=')[1]]) assert.equal(stored.includes(secret),false);
 assert.equal((await f.call('live?room=other',{},cookie)).status,403);
 assert.equal((await f.call('session',{},cookie,'https://evil.test')).status,403);
 assert.equal((await f.call('logout',{},cookie,'https://evil.test')).status,403);
 assert.equal((await f.call('session',{},cookie+'; '+cookie)).status,401);
 assert.equal((await worker.fetch(f.request('session',{},cookie),{...f.env,PASSWORD:'changed'})).status,401);
 assert.equal((await worker.fetch(f.request('session',{},cookie),{...f.env,PASSWORD:''})).status,401);
 const custom=new Request('https://example.test/custom/view/session',{method:'POST',headers:{Origin:'https://example.test',Cookie:cookie}});
 assert.equal((await worker.fetch(custom,{...f.env,PANEL_PATH:'/custom/view'})).status,401);
 assert.equal((await worker.fetch(new Request('https://example.test/panel'),{PANEL_PATH:'off'})).status,404);
 assert.equal((await worker.fetch(new Request('https://example.test/custom/view'),{PANEL_PATH:'/custom/view'})).status,200);
});

test('panel lifetime is fixed, optional persistence lasts seven days and expired records are collected',async t=>{
 let now=Date.now();t.mock.method(Date,'now',()=>now);
 const f=panelFixture();
 for(const remember of [false,true]) {
  const response=await f.call('auth',{password:f.env.PASSWORD,remember});
  const setCookie=response.headers.get('Set-Cookie'),cookie=setCookie.split(';')[0];
  const {expires}=await response.json();
  assert.equal(expires-now,(remember?7*24:12)*3600_000);
  assert.equal(setCookie.includes('Max-Age=604800'),remember);
  now=expires-1;
  assert.equal((await (await f.call('session',{},cookie)).json()).expires,expires);
  now=expires;
  assert.equal((await f.call('session',{},cookie)).status,401);
  f.hibernate();await f.broker.alarm();
  assert.equal([...f.brokerState.values.keys()].filter(k=>k.startsWith('panel_browser:')).length,0);
  assert.equal(await f.brokerState.state.storage.getAlarm(),null);
 }
});

test('panel room grants survive hibernation, enforce expiry, and logout revokes every tab',async t=>{
 const NativeResponse=Response;
 t.mock.method(globalThis,'Response',class extends NativeResponse {
  constructor(body,init={}) {super(body,init.status===101?{...init,status:200}:init);if(init.status===101)Object.defineProperty(this,'status',{value:101});this.webSocket=init.webSocket;}
 });
 const f=panelFixture();
 // Use the production upgrade and watch flow with only the runtime socket pair stubbed.
 const pairDescriptor=Object.getOwnPropertyDescriptor(globalThis,'WebSocketPair');
 Object.defineProperty(globalThis,'WebSocketPair',{value:class {constructor(){this[0]={};this[1]=f.roomState.socket();}},configurable:true});
 t.after(()=>{if(pairDescriptor)Object.defineProperty(globalThis,'WebSocketPair',pairDescriptor);else delete globalThis.WebSocketPair;});
 f.roomState.state.acceptWebSocket=()=>{};
 let now=Date.now();t.mock.method(Date,'now',()=>now);
 await f.room.webSocketMessage(f.roomState.socket(),JSON.stringify(join('alpha')));
 async function login() {return (await f.call('auth',{password:f.env.PASSWORD,remember:true})).headers.get('Set-Cookie').split(';')[0];}
 async function watch(cookie) {
  assert.equal((await f.call('live',{},cookie,undefined,{'X-Hole-Panel-Proof':'forged','X-Hole-Panel-Expires':'9999999999999'})).status,101);
  const ws=f.roomState.state.getWebSockets().at(-1);
  await f.room.webSocketMessage(ws,JSON.stringify({type:'watch'}));
  assert.equal(ws.messages.at(-1).type,'panel_snapshot');return ws;
 }
 const cookie=await login();
 assert.equal((await f.call('room',{room:'demo',token:'room-secret'},cookie)).status,200);
 const a=await watch(cookie),b=await watch(cookie);
 f.hibernate();
 f.room.roomToken='changed';f.room.loaded=true;
 assert.equal((await f.call('live',{},cookie)).status,403);
 assert.equal((await (await f.call('session',{},cookie)).json()).roomExpired,true);
 f.hibernate();
 let release,started;
 const ready=new Promise(resolve=>{started=resolve}),fetchRoom=f.room.fetch.bind(f.room);
 f.room.fetch=async request=>{
  if(request.headers.has('Upgrade')) {started();await new Promise(resolve=>{release=resolve});}
  return fetchRoom(request);
 };
 const lateUpgrade=f.call('live',{},cookie);await ready;
 const logout=await f.call('logout',{},cookie);
 release();assert.equal((await lateUpgrade).status,401);
 f.room.fetch=fetchRoom;
 assert.equal(f.roomState.state.getWebSockets().filter(ws=>ws.attachment?.panel).length,0);
 assert.equal(logout.status,200);assert.match(logout.headers.get('Set-Cookie'),/Max-Age=0/);
 assert.equal(a.closed,true);assert.equal(b.closed,true);
 assert.equal((await f.call('session',{},cookie)).status,401);
 assert.equal((await f.call('live',{},cookie)).status,401);
 const next=await login();
 await f.call('room',{room:'demo',token:'room-secret'},next);
 const switched=await watch(next);
 await f.call('room',{room:''},next);
 assert.equal(switched.closed,true);
 assert.equal((await (await f.call('session',{},next)).json()).room,'');
 assert.equal((await f.call('live',{},next)).status,409);
 await f.call('room',{room:'demo',token:'room-secret'},next);
 const expiring=await watch(next);
 now=(await (await f.call('session',{},next)).json()).expires;
 f.room.broadcastPanel();assert.equal(expiring.closed,true);
 const interrupted=await login();
 await f.call('room',{room:'demo',token:'room-secret'},interrupted);
 const retained=await watch(interrupted);
 t.mock.method(f.room,'fetch',async()=>{throw new Error('room RPC unavailable')});
 assert.equal((await f.call('logout',{},interrupted)).status,503);
 assert.equal((await f.call('session',{},interrupted)).status,401);
 assert.equal(retained.closed,false);
 assert.ok(await f.brokerState.state.storage.getAlarm()<=now+30_000);
 f.hibernate();await f.broker.alarm();
 assert.equal(retained.closed,true);
 assert.equal([...f.brokerState.values.keys()].filter(k=>k.startsWith('panel_browser:')).length,0);
});

test('panel reauthentication replaces old sessions and refuses unbounded retained sessions',async()=>{
 const f=panelFixture();
 const first=(await f.call('auth',{password:f.env.PASSWORD,remember:true})).headers.get('Set-Cookie').split(';')[0];
 const second=await f.call('auth',{password:f.env.PASSWORD},first);
 assert.equal(second.status,200);assert.equal(second.headers.get('Set-Cookie').includes('Max-Age='),false);
 assert.equal((await f.call('session',{},first)).status,401);
 for(let i=0;i<1024;i++) f.brokerState.values.set('panel_browser:fixture'+i,{expires:Date.now()+60_000});
 assert.equal((await f.call('auth',{password:f.env.PASSWORD})).status,429);
});

// Execute the page's actual login/restore handlers; rendering and browser I/O
// are substituted so these tests can exercise delayed replies deterministically.
test('panel identifies Minecraft only from explicit metadata and decodes world names',()=>{
 const script=PANEL_PAGE.match(/<script>([\s\S]*?)<\/script>/)[1];
 const context={atob,TextDecoder,Uint8Array};
 runInNewContext(script.slice(script.indexOf('  function minecraftServices('),script.indexOf('  const signalLabel')),context);
 const service={id:'minecraft.0123456789abcdef.'+Buffer.from('海岛世界🌊').toString('base64url'),protocol:'tcp'};
 assert.equal(context.minecraftServices({services:[service]}).length,0);
 service.kind='minecraft';
 assert.equal(context.minecraftServices({services:[service]}).length,1);
 assert.equal(context.minecraftTitle(service),'海岛世界🌊');
 assert.equal(context.minecraftTitle({...service,id:'minecraft.0123456789abcdef._w'}),'minecraft.0123456789abcdef._w');
 assert.equal(context.minecraftTitle({...service,id:'minecraft.0123456789abcdef.'}),'Minecraft 世界');
 assert.equal(context.minecraftServices({}).length,0);
 assert.equal(context.minecraftServices({services:[{...service,protocol:'udp'}]}).length,0);
});

function panelGraphFixture() {
 const script=PANEL_PAGE.match(/<script>([\s\S]*?)<\/script>/)[1],context={};
 runInNewContext(script.slice(script.indexOf('  const CLOUD'),script.indexOf('  const nodes'))+
  script.slice(script.indexOf('  const signalLabel'),script.indexOf('  function rows(')),context);
 return context;
}

test('panel keeps overlapping direct links and relay segments distinct in both layouts and endpoint orders',()=>{
 const page=panelGraphFixture();
 for(const [a,b] of [['A','B'],['B','A']]) {
  const snapshot={members:['A','B','C'].map(device_name=>({device_name})),links:[{from:'A',to:'B'},{from:'B',to:'C'}],
   transports:[{id:'ab',a,b,established:['hole','hole'],route:{kind:'hole',device:'C'}},
    {id:'bc',a:'B',b:'C',established:['direct','direct'],route:null}]};
  const edges=[...page.buildEdges(snapshot).values()];
  const overlapping=edges.filter(e=>[e.from,e.to].sort().join(',')==='device:B,device:C');
  assert.equal(overlapping.length,2);
  assert.equal(page.edgeCount(edges),2);
  for(const compact of [false,true]) {
   const positions={'device:B':{x:20,y:30},'device:C':{x:20,y:330}};
   const paths=overlapping.map(edge=>page.edgePath(edge,positions[edge.from],positions[edge.to],compact));
   assert.equal(new Set(paths).size,2,'the direct link must not cover the relay segment');
  }
  const reversed=page.buildEdges({...snapshot,transports:[...snapshot.transports].reverse()});
  for(const edge of edges)assert.equal(reversed.get(edge.id).lane,edge.lane,'snapshot order must not move the lanes');
 }
});

function panelPageFixture(fetcher) {
 const elements=new Map(),events=new Map(),sockets=[],timers=new Map();let timerID=0;
 const $=id=>{
  if(!elements.has(id)) elements.set(id,{value:'',checked:false,disabled:false,hidden:false,dataset:{},handlers:{},
   addEventListener(type,fn){this.handlers[type]=fn},removeAttribute(){},setAttribute(){},focus(){},reset(){}});
  return elements.get(id);
 };
 const context={$,URL,AbortController,Date,JSON,Math,Promise,location:{pathname:'/panel',href:'https://example.test/panel',protocol:'https:'},
  roomName:'',watching:false,expiryTimer:null,requestChain:Promise.resolve(),socket:null,retryTimer:null,connectionTimer:null,epoch:0,retryAttempt:0,snapshot:null,
  fetch:fetcher,setTimeout(fn,delay){const id=++timerID;timers.set(id,{fn,delay});return id},clearTimeout(id){timers.delete(id)},
  busy(id,value){$(id).disabled=value},screen(page){context.page=page},setState(){},hidePasswords(){},
  resetGraph(){context.snapshot=null},render(data){context.snapshot=data},addEventListener(type,fn){events.set(type,fn)},
  WebSocket:class {constructor(...args){this.args=args;this.sent=[];sockets.push(this)}close(){this.closed=true}send(data){this.sent.push(JSON.parse(data))}}};
 const script=PANEL_PAGE.match(/<script>([\s\S]*?)<\/script>/)[1];
 new Function(script); // Also parse the complete inline script, not only its handlers.
 runInNewContext(script.slice(script.indexOf('  function api('),script.lastIndexOf('})();')),context);
 const settle=async()=>{await context.requestChain;await new Promise(resolve=>setImmediate(resolve));};
 const submit=id=>$(id).handlers.submit({preventDefault(){}});
 return {$,events,sockets,timers,context,settle,submit};
}

test('panel page restores the room without secrets and pagehide preserves the server session',async()=>{
 const f=panelFixture();let cookie='';const requests=[];
 const fetcher=async(path,options)=>{
  const action=path.split('/').at(-1),body=JSON.parse(options.body);requests.push({action,body});
  const response=await f.call(action,body,cookie),setCookie=response.headers.get('Set-Cookie');
  if(setCookie)cookie=setCookie.split(';')[0];return response;
 };
 const page=panelPageFixture(fetcher);await page.settle();
 assert.equal(page.context.page,'auth');
 page.$('password').value=f.env.PASSWORD;page.$('remember').checked=true;
 await page.submit('authForm');assert.equal(page.context.page,'room');assert.equal(page.$('password').value,'');
 await f.room.webSocketMessage(f.roomState.socket(),JSON.stringify(join('alpha')));
 page.$('roomId').value='demo';page.$('token').value='room-secret';
 await page.submit('roomForm');assert.equal(page.$('token').value,'');assert.equal(page.sockets.length,1);
 page.sockets[0].onopen();assert.deepEqual(page.sockets[0].sent,[{type:'watch'}]);
 assert.equal(page.sockets[0].args.length,1);assert.equal(String(page.sockets[0].args[0]),'wss://example.test/panel/live');
 page.events.get('pagehide')();assert.equal(page.sockets[0].closed,true);
 assert.equal(requests.some(r=>r.action==='logout'),false);
 const reopened=panelPageFixture(fetcher);await reopened.settle();
 assert.equal(reopened.context.roomName,'demo');assert.equal(reopened.sockets.length,1);
 assert.equal(requests.filter(r=>r.action==='auth').length,1);assert.equal(requests.filter(r=>r.action==='room').length,1);
 reopened.events.get('pagehide')();reopened.events.get('pageshow')({persisted:true});await reopened.settle();
 assert.equal(reopened.sockets.length,2);
 await reopened.$('logout').handlers.click();assert.equal(reopened.context.page,'auth');
 assert.equal((await f.call('session',{},cookie)).status,401);
});

test('panel page serializes logout after a delayed login and reports a failed logout',async()=>{
 let release;const requests=[];
 const page=panelPageFixture(async(path)=>{
  const action=path.split('/').at(-1);requests.push(action);
  if(action==='session')return new Response(null,{status:401});
  if(action==='auth')await new Promise(resolve=>{release=resolve});
  return Response.json({room:'',expires:Date.now()+3600_000});
 });
 await page.settle();page.$('password').value='password';
 const login=page.submit('authForm');await new Promise(resolve=>setImmediate(resolve));
 const logout=page.$('logout').handlers.click();release();await login;await logout;
 assert.deepEqual(requests,['session','auth','logout']);assert.equal(page.context.page,'auth');
 assert.equal(page.sockets.length,0);
 const offline=panelPageFixture(async(path)=>{
  if(path.endsWith('/logout'))throw new Error('offline');
  return Response.json({room:'',expires:Date.now()+3600_000});
 });
 await offline.settle();await offline.$('logout').handlers.click();
 assert.equal(offline.context.page,'room');assert.match(offline.$('roomError').textContent,/退出未完成/);
 assert.equal(offline.$('logout').disabled,false);
});

test('panel watches only an existing authenticated room and receives event-driven state across hibernation', async () => {
 const f=fixture(), r=new Room(f.state,{}), empty=f.socket();
 empty.serializeAttachment({panel:'pending'});
 await r.webSocketMessage(empty,JSON.stringify({type:'watch',token:'room-secret'}));
 assert.equal(empty.messages[0].code,'panel_auth_failed');
 assert.equal(f.values.has('token'),false);
 const a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify(join('alpha',['one'])));
 const bad=f.socket();bad.serializeAttachment({panel:'pending'});
 await r.webSocketMessage(bad,JSON.stringify({type:'watch',token:'wrong'}));
 assert.equal(bad.messages[0].code,'panel_auth_failed');
 assert.equal(bad.messages.some(m=>m.type==='panel_snapshot'),false);
 const watch=f.socket();watch.serializeAttachment({panel:'pending'});
 await r.webSocketMessage(watch,JSON.stringify({type:'watch',token:'room-secret'}));
 assert.deepEqual(watch.messages.at(-1).members.map(m=>m.device_name),['alpha']);
 await r.webSocketMessage(b,JSON.stringify(join('beta',[],['one'])));
 let snapshot=watch.messages.at(-1);
 assert.deepEqual(snapshot.members.map(m=>m.device_name),['alpha','beta']);
 assert.equal(snapshot.transports.length,1);
 assert.deepEqual(snapshot.transports[0].established,['','']);
 const record=[...r.ice.records.values()][0];
 for(const ws of [a,b]) await r.webSocketMessage(ws,JSON.stringify({type:'transport_established',transport_id:record.id,transport_generation:record.generation,path_type:'direct'}));
 snapshot=watch.messages.at(-1);
 assert.deepEqual(snapshot.transports[0].established,['direct','direct']);
 assert.equal(snapshot.transports[0].route,null);
 const oldRouteState={phase:record.phase,hole_version:record.hole_version,round_mode:record.round_mode,round:record.round,relay_plan:record.relay_plan,hole:record.hole,peer_relay:record.peer_relay};
 record.phase='relay_udp';
 snapshot=r.panelSnapshot();
 assert.deepEqual(snapshot.transports[0].route,{kind:'turn'});
 record.hole_version='hole-relay-v1';record.round_mode=true;record.round=1;
 record.relay_plan=[{device:'relay-node',a:'hole',b:'hole'}];record.hole={device:'relay-node',route:'private-route'};
 snapshot=r.panelSnapshot();
 assert.deepEqual(snapshot.transports[0].route,{kind:'hole',device:'relay-node'});
 record.peer_relay={device:'relay-node',route:'private-route'};
 snapshot=r.panelSnapshot();
 assert.deepEqual(snapshot.transports[0].route,{kind:'peer_relay',device:'relay-node'});
 assert.equal(JSON.stringify(snapshot).includes('private-route'),false);
 Object.assign(record,oldRouteState);
 assert.equal(JSON.stringify(snapshot).includes('room-secret'),false);
 assert.equal(JSON.stringify(snapshot).includes('127.0.0.1'),false);
 const before=watch.messages.length;
 await r.webSocketMessage(a,JSON.stringify({type:'transport_sync'}));
 assert.equal(watch.messages.length,before);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'direct',reason:'retry'}));
 assert.equal(watch.closed,false);
 assert.equal(watch.messages.at(-1).type,'panel_snapshot');
 assert.equal(watch.messages.at(-1).transports[0].generation,'2');
 await resumed.webSocketMessage(a,JSON.stringify({type:'transport_failed',transport_id:record.id,transport_generation:'2',code:'peer_identity_mismatch'}));
 assert.equal(watch.messages.at(-1).transports[0].blocked,'peer_identity_mismatch');
 await resumed.webSocketMessage(watch,JSON.stringify({type:'leave'}));
 assert.equal(watch.closed,true);
 assert.equal(resumed.members.size,2);
});
const endpoint={protocol:'tcp',addr:'127.0.0.1',port:22};
function join(name,provide=[],consume=[]){return{type:'join',signal_version:2,auth_mode:'shared-secret',runtime_id:(name==='alpha'?'a':'b').repeat(32),transport_epoch:'1',transport_profiles:[ICE_PROFILE],session_versions:[2],cert_fingerprint:(name==='alpha'?'a':'b').repeat(64),room:'fixture',token:'room-secret',device_name:name,provide:provide.map(id=>({id,service:endpoint})),consume:consume.map(id=>({id,expose:{addr:'127.0.0.1',port:18080}}))}}
async function pair(){const f=fixture();const r=new Room(f.state,{});const a=f.socket(),b=f.socket();await r.webSocketMessage(a,JSON.stringify(join('alpha',['one','two'])));await r.webSocketMessage(b,JSON.stringify(join('beta',[],['one','two'])));return{...f,r,a,b,record:[...r.ice.records.values()][0]}}

function modernJoin(name, provide = [], consume = []) {
 return {...join(name, provide, consume), core_version:'1.1.0', certificate:'fixture-'+name};
}
function published(declaration) {
 return {...declaration,provide:declaration.provide.map(p=>({...p,discovery:'minecraft'}))};
}

test('world voice isolates groups, updates authorization and survives hibernation',async()=>{
 const f=await modernFixture(),sockets=['alpha','beta','gamma','delta'].map(()=>f.socket());
 const groups=['a'.repeat(64),'a'.repeat(64),'b'.repeat(64),''];
 const declarations=['alpha','beta','gamma','delta'].map((name,i)=>({...modernJoin(name),live_config:true,voice:true,voice_group:groups[i]}));
 for(let i=0;i<4;i++)await f.r.webSocketMessage(sockets[i],JSON.stringify(declarations[i]));
 const active=r=>[...r.ice.records.values()].filter(x=>x.online && x.voice);
 assert.equal(active(f.r).length,1);
 assert.deepEqual([active(f.r)[0].a,active(f.r)[0].b],['alpha','beta']);
 assert.equal(active(f.r)[0].voice_group,groups[0]);
 assert.equal(JSON.stringify(f.r.panelSnapshot()).includes(groups[0]),false);
 const restored=f.newRoom();await restored.loadState();
 await restored.webSocketMessage(sockets[1],JSON.stringify(memberUpdate({...declarations[1],voice_group:groups[2]},1)));
 assert.equal(active(restored).length,1);
 assert.deepEqual([active(restored)[0].a,active(restored)[0].b],['beta','gamma']);
 const record=active(restored)[0],revision=BigInt(record.voice_revision);
 for(const name of [record.a,record.b])await restored.webSocketMessage(sockets[name==='beta'?1:2],JSON.stringify({type:'transport_established',transport_id:record.id,transport_generation:record.generation,path_type:'direct'}));
 await restored.webSocketMessage(sockets[1],JSON.stringify(memberUpdate({...declarations[1],voice_group:'c'.repeat(64)},2)));
 assert.equal(active(restored).length,0);
 await restored.webSocketMessage(sockets[2],JSON.stringify(memberUpdate({...declarations[2],voice_group:'c'.repeat(64)},1)));
 assert.equal(active(restored).length,1);
 assert.ok(BigInt(active(restored)[0].voice_revision)>revision);
 assert.equal(active(restored)[0].voice_group,'c'.repeat(64));
});
async function modernFixture() {
 const f=fixture();
 const keys=await webcrypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},true,['sign','verify']);
 const encrypt=await webcrypto.subtle.importKey('spki',await webcrypto.subtle.exportKey('spki',keys.publicKey),{name:'ECDH',namedCurve:'P-256'},false,[]);
 const newRoom=()=>{
  const r=new Room(f.state,{});
  // These lifecycle tests substitute certificate lookup; graph signatures and
  // notice encryption still use real keys and the production implementation.
  r.control.certificate=async()=>({verify:keys.publicKey,encrypt});
  return r;
 };
 const r=newRoom();await r.loadState();await r.ice.setRoomName('fixture');
 const nodeFrame=async(member,neighbours=[],revision=1)=>{
  const node={device_name:member.device_name,runtime_id:member.runtime_id,core_version:member.core_version,worker_version:'1.1.0',
   revision:String(revision),transport_epoch:member.transport_epoch,signal_online:true,resident:true,neighbours};
  const frame={id:crypto.randomUUID().replaceAll('-',''),room:'fixture',origin:member.device_name,runtime:member.runtime_id,kind:'node',
   body:Buffer.from(JSON.stringify(node)).toString('base64'),certificate:member.certificate,hops:16};
  frame.signature=Buffer.from(await webcrypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},keys.privateKey,roomSigningBytes(frame))).toString('base64');
  return frame;
 };
 return {...f,r,newRoom,nodeFrame};
}

function memberUpdate(declaration, revision) {
 return {...declaration,type:'member_update',member_revision:String(revision),request_id:revision.toString(16).padStart(32,'0')};
}
test('room notices survive carrier loss during encryption and retry without duplicating batches',async()=>{
 const {r,socket}=await modernFixture(),a=socket(),b=socket(),control=r.control;
 r.registered.set('alpha',{member:modernJoin('alpha'),ws:a});
 r.registered.set('beta',{member:modernJoin('beta'),ws:b});
 control.rebuild();
 control.enqueue('alpha',{type:'notice_test',value:'alpha'});
 control.enqueue('beta',{type:'notice_test',value:'beta'});
 const queuedBytes=control.noticeBytes,sign=control.signNotice.bind(control);
 let entered,release;
 const signing=new Promise(resolve=>{entered=resolve}),resume=new Promise(resolve=>{release=resolve});
 control.signNotice=async(...args)=>{
  const frame=await sign(...args);
  if(args[0]==='alpha'){entered();await resume;}
  return frame;
 };
 const pending=control.flush();
 await signing;
 const inFlightCount=control.notices.length,inFlightBytes=control.noticeBytes;
 a.close();
 const overlapping=control.flush();
 release();
 await Promise.all([pending,overlapping]);
 assert.equal(inFlightCount,2);
 assert.equal(inFlightBytes,queuedBytes);
 assert.equal(b.messages.length,1);
 assert.equal(control.notices.length,1);
 assert.equal(control.notices[0].target,'alpha');
 assert.equal(control.noticeBytes,control.notices[0].size);

 const replacement=socket(),send=replacement.send;
 r.registered.get('alpha').ws=replacement;
 replacement.send=()=>{throw new Error('carrier closed while sending')};
 control.enqueue('beta',{type:'notice_test',value:'second'});
 await control.flush();
 assert.equal(b.messages.length,2);
 assert.equal(control.notices.length,1);
 replacement.send=send;
 await control.flush();
 assert.equal(replacement.messages.length,1);
 assert.equal(control.notices.length,0);
 assert.equal(control.noticeBytes,0);

 control.enqueue('alpha',{type:'notice_test',value:'old identity'});
 r.registered.get('alpha').member={...modernJoin('alpha'),runtime_id:'f'.repeat(32)};
 await control.flush();
 assert.equal(replacement.messages.length,1);
 assert.equal(control.notices.length,0);
 assert.equal(control.noticeBytes,0);
});

test('room snapshots retry failed sends and signatures without blocking other recipients',async()=>{
 const {r,socket}=await modernFixture(),a=socket(),b=socket(),control=r.control;
 r.registered.set('alpha',{member:modernJoin('alpha'),ws:a});
 r.registered.set('beta',{member:modernJoin('beta'),ws:b});
 control.rebuild();await control.ensureKey();
 const send=a.send;
 a.send=()=>{throw new Error('closed socket')};
 await control.publishSnapshot();
 assert.equal(a.messages.length,0);
 assert.equal(b.messages.length,1);
 assert.equal(control.lastSnapshot,'');
 a.send=send;
 const sign=control.signNotice.bind(control);
 control.signNotice=async(...args)=>{const frame=await sign(...args);a.close();return frame;};
 await control.publishSnapshot();
 assert.equal(b.messages.length,2);
 assert.equal(control.lastSnapshot,'');
 const replacement=socket();
 r.registered.get('alpha').ws=replacement;
 control.signNotice=sign;
 await control.publishSnapshot();
 assert.equal(replacement.messages.length,1);
 assert.equal(b.messages.length,3);
 const published=control.lastSnapshot;
 await control.publishSnapshot();
 assert.equal(b.messages.length,3);

 r.registered.get('alpha').member.voice=true;
 control.signNotice=async()=>{throw new Error('signing failed')};
 await assert.rejects(control.publishSnapshot(),/signing failed/);
 assert.equal(control.lastSnapshot,published);
 control.signNotice=sign;
 await control.publishSnapshot();
 assert.equal(replacement.messages.length,2);
 assert.equal(b.messages.length,4);
 assert.notEqual(control.lastSnapshot,published);
});

test('online declarations preserve transport, enforce identity and survive hibernation',async()=>{
 const {r,a,b,record,state}=await pair(),generation=record.generation;
 const send=message=>r.webSocketMessage(b,JSON.stringify(message));
 const update=memberUpdate(join('beta',[],['two']),1);
 await send(update);
 assert.equal(b.closed,false);
 assert.equal(record.generation,generation);
 assert.deepEqual(record.mappings.map(m=>m.mapping_id),['two']);
 const ack=b.messages.findIndex(m=>m.type==='member_updated');
 assert.ok(ack>=0 && b.messages.slice(ack+1).some(m=>m.type==='transport_ready'));
 for(const override of [{token:'wrong'},{device_name:'alpha'},{runtime_id:'f'.repeat(32)},{cert_fingerprint:'f'.repeat(64)},{transport_epoch:'0'},{provide:[{id:'broken'}]},{member_revision:'0'},{member_revision:'1e3'}]) {
  await send({...memberUpdate(join('beta'),2),...override});
  assert.equal(b.messages.at(-1).type,'error');
  assert.deepEqual(record.mappings.map(m=>m.mapping_id),['two']);
 }
 const resumed=new Room(state,{});
 await resumed.webSocketMessage(b,JSON.stringify(update));
 assert.equal(b.messages.at(-1).type,'member_updated');
 await resumed.webSocketMessage(b,JSON.stringify({...update,request_id:'f'.repeat(32)}));
 assert.equal(b.messages.at(-1).code,'stale_member_revision');
 await resumed.webSocketMessage(b,JSON.stringify(memberUpdate(join('beta'),2)));
 assert.equal(a.messages.at(-1).reason,'mapping_removed');
 assert.equal(b.closed,false);
});

test('voice updates reuse negotiated live transport and restart old peers',async()=>{
 for(const live of [false,true]) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const left={...join('alpha',['one']),live_config:true,voice:true};
  const right={...join('beta',[],['one']),live_config:live};
  await r.webSocketMessage(a,JSON.stringify(left));await r.webSocketMessage(b,JSON.stringify(right));
  const record=[...r.ice.records.values()][0],generation=record.generation;
  for(const [revision,voice] of [[1,true],[2,false],[3,true]]) {
   await r.webSocketMessage(b,JSON.stringify(memberUpdate({...right,voice},revision)));
   assert.equal(record.voice,voice);
   assert.equal(record.voice_revision,String(revision+1));
   assert.equal(record.generation,live?generation:String(BigInt(generation)+BigInt(revision)));
   assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).live_config,live);
  }
 }
});

test('online endpoint changes advance only the affected device transports',async()=>{
 const {r,b,record}=await pair(),generation=record.generation;
 await r.webSocketMessage(b,JSON.stringify(memberUpdate({...join('beta',[],['one','two']),transport_epoch:'2'},1)));
 assert.equal(record.generation,String(BigInt(generation)+1n));
});

const payload={iceServers:[{urls:['stun:stun.cloudflare.com:3478']},{urls:['turn:turn.cloudflare.com:3478?transport=udp','turn:turn.cloudflare.com:3478?transport=tcp','turn:turn.cloudflare.com:80?transport=tcp','turns:turn.cloudflare.com:5349?transport=tcp','turns:turn.cloudflare.com:443?transport=tcp'],username:'temporary-user',credential:'temporary-secret'}]};

test('fixed endpoints validate addresses and enforce exclusive negotiation',()=>{
 for(const address of ['203.0.113.10:50000','[2001:db8::1]:65535','gateway.example:1','localhost:443']) assert.equal(validFixedEndpoint(address),true,address);
 for(const address of ['',':44','0.0.0.0:44','[::]:44','[0:0:0:0:0:0:0:0]:44','[::ffff:0.0.0.0]:44','[not:ip]:44','a:0','a:65536','-bad:4','bad/name:44','a:4\n']) assert.equal(validFixedEndpoint(address),false,address);
 const fixed={...join('alpha'),transport_profiles:[ICE_PROFILE,FIXED_PROFILE],fixed_endpoint:'gateway.example:50000'};
 assert.ok(iceJoinFields(fixed));
 assert.equal(iceJoinFields({...fixed,relay_only:true}),null);
 assert.equal(iceJoinFields({...fixed,transport_profiles:[ICE_PROFILE]}),null);
 assert.equal(negotiateProfile(fixed,{transport_profiles:[ICE_PROFILE,FIXED_PROFILE]}),FIXED_PROFILE);
 assert.equal(negotiateProfile(fixed,{transport_profiles:[ICE_PROFILE]}),null);
 assert.equal(negotiateProfile(fixed,{transport_profiles:[ICE_PROFILE,FIXED_PROFILE],relay_only:true}),null);
});

test('fixed-only retries alternate listeners, coalesce generations and survive hibernation',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={transport_profiles:[ICE_PROFILE,FIXED_PROFILE],relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 const ja={...join('alpha',['one']),...caps,fixed_endpoint:'alpha.example:50000'};
 const jb={...join('beta',[],['one']),...caps,fixed_endpoint:'[2001:db8::2]:40000'};
 await r.webSocketMessage(a,JSON.stringify(ja));await r.webSocketMessage(b,JSON.stringify(jb));
 const record=[...r.ice.records.values()][0];
 const check=(listener,delay)=>{
  for(const s of [a,b]) {
   const ready=s.messages.filter(m=>m.type==='transport_ready').at(-1);
   assert.equal(ready.profile,FIXED_PROFILE);assert.equal(ready.phase,'fixed');assert.equal(ready.listener_id,listener);
   assert.equal(ready.initiator_id,ready.dialer_id);assert.notEqual(ready.dialer_id,listener);
   assert.equal(ready.fixed_endpoint,listener==='alpha'?ja.fixed_endpoint:jb.fixed_endpoint);
   assert.equal(ready.retry_after_ms,delay);assert.equal(ready.relay_round,undefined);assert.equal(ready.relay_pairing,undefined);
  }
 };
 check('alpha',0);
 for(let i=0;i<6;i++) {
  const message={type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'fixed'};
  const previous=record.generation;
  await r.webSocketMessage(a,JSON.stringify(message));await r.webSocketMessage(b,JSON.stringify(message));
  assert.equal(record.generation,String(BigInt(previous)+1n));
  check(i%2===0?'beta':'alpha',Math.min(15000,1000*2**i));
 }
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(a,JSON.stringify({type:'transport_sync'}));check('alpha',15000);
 await resumed.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'relay_udp'}));
 assert.equal(a.messages.at(-1).code,'invalid_phase');
 await resumed.webSocketMessage(a,JSON.stringify({type:'ice_candidate',transport_id:record.id,transport_generation:record.generation,to_peer_id:'beta',candidate:'1 1 udp 1 127.0.0.1 3333 typ host'}));
 assert.equal(a.messages.at(-1).code,'invalid_phase');
 await resumed.webSocketMessage(a,JSON.stringify({type:'turn_request',request_id:'fixed-test'}));
 assert.equal(a.messages.at(-1).code,'transport_not_authorized');
 const replacement=f.socket();
 await resumed.webSocketMessage(replacement,JSON.stringify({...ja,fixed_endpoint:'new.example:60000'}));
 const ready=b.messages.filter(m=>m.type==='transport_ready').at(-1);
 assert.equal(ready.fixed_endpoint,'new.example:60000');assert.ok(BigInt(ready.transport_generation)>BigInt(record.generation));
});

test('one fixed listener never falls back, relay-only conflict is explicit',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={transport_profiles:[ICE_PROFILE,FIXED_PROFILE]};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps,fixed_endpoint:'public.example:40000'}));
 const jb={...join('beta',[],['one']),...caps};
 await r.webSocketMessage(b,JSON.stringify(jb));
 const record=[...r.ice.records.values()][0];
 for(let i=0;i<3;i++) {
  const ready=b.messages.filter(m=>m.type==='transport_ready').at(-1);
  assert.equal(ready.listener_id,'alpha');assert.equal(ready.dialer_id,'beta');assert.equal(ready.profile,FIXED_PROFILE);
  await r.webSocketMessage(b,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,phase:'fixed'}));
 }
 const replacement=f.socket();
 await r.webSocketMessage(replacement,JSON.stringify({...jb,relay_only:true}));
 assert.ok(a.messages.some(m=>m.code==='fixed_relay_conflict'));
 assert.ok(replacement.messages.some(m=>m.code==='fixed_relay_conflict'));
 assert.equal(r.ice.records.size,0);
});

test('transport profiles reject removed IPv6 and absent signal version',()=>{
 assert.equal(negotiateProfile({transport_profiles:[ICE_PROFILE]},{transport_profiles:[ICE_PROFILE]}),ICE_PROFILE);
 assert.equal(negotiateProfile({transport_profiles:[ICE_PROFILE]},{}),null);
 assert.equal(iceJoinFields({...join('alpha'),transport_profiles:['legacy-ipv6-quic-v2']}),null);
 assert.equal(iceJoinFields({...join('alpha'),signal_version:undefined}),null);
 assert.equal(iceJoinFields({...join('alpha'),auth_mode:'device-proof'}),null);
 assert.equal(iceJoinFields({...join('alpha'),transport_epoch:1}),null);
});

test('voice-only members receive a versioned room state and transport', async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha'),voice:true}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta'),voice:true}));
 const state=a.messages.filter(m=>m.type==='room_state').at(-1);
 assert.deepEqual(state.members,[{device_name:'alpha',voice:true},{device_name:'beta',voice:true}]);
 const ready=a.messages.filter(m=>m.type==='transport_ready').at(-1);
 assert.equal(ready.voice,true); assert.deepEqual(ready.mappings,[]);
 assert.equal([...r.ice.records.values()].length,1);
});

test('voice participant capacity does not change ordinary mapping capacity', async()=>{
 const f=fixture(),r=new Room(f.state,{});
 for(let i=0;i<9;i++) { const s=f.socket(); const name=`voice-${String(i).padStart(2,'0')}`; await r.webSocketMessage(s,JSON.stringify({...join(name),voice:true})); }
 const records=[...r.ice.records.values()].filter(record=>record.voice===true);
 assert.equal(records.length,28);
 const ninth=[...f.state.getWebSockets()].find(s=>s.messages.some(m=>m.type==='error'&&m.code==='voice_capacity'));
 assert.ok(ninth);
});

test('voice-only members reject removed IPv6 profiles', async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha'),voice:true,transport_profiles:['legacy-ipv6-quic-v2']}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta'),voice:true,transport_profiles:['legacy-ipv6-quic-v2']}));
 assert.equal(r.ice.records.size,0);
 assert.equal(a.messages.at(-1).code,'invalid_join');
 assert.equal(b.messages.at(-1).code,'invalid_join');
});
test('complete relay pairing covers cross types, survives hibernation and coalesces restarts',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const capabilities={relay_policy:RELAY_POLICY,relay_pairing:'all-pairs-v1'};
 const left=['tcp_80','udp'],right=['udp','tcp_80','tcp','tls_443','tls'];
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...capabilities,relay_order:left}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...capabilities,relay_order:right}));
 const record=[...r.ice.records.values()][0],pairs=relayPairs(left,right),seen=new Set();
 assert.equal(pairs.length,10);
 const phases={udp:'relay_udp',tcp_80:'relay_tcp_80',tcp:'relay_tcp',tls_443:'relay_tls_443',tls:'relay_tls'};
 for(let round=1;round<=pairs.length;round++) {
  const previous=record.generation, message={type:'transport_restart',transport_id:record.id,expected_generation:previous,relay_round:round};
  await r.webSocketMessage(a,JSON.stringify(message));
  await r.webSocketMessage(b,JSON.stringify(message));
  assert.equal(record.generation,String(BigInt(previous)+1n));
  assert.equal(a.messages.at(-1).relay_pairing,'all-pairs-v1');
  const [i,j]=pairs[round-1];
  assert.equal(a.messages.at(-1).phase,phases[left[i]]);
  assert.equal(b.messages.at(-1).phase,phases[right[j]]);
  seen.add(`${i}/${j}`);
 }
 assert.equal(seen.size,10);
 const resumed=new Room(f.state,{});
 await resumed.webSocketMessage(a,JSON.stringify({type:'transport_sync'}));
 assert.equal(a.messages.at(-1).relay_pairing,'all-pairs-v1');
 assert.equal(a.messages.at(-1).relay_round,10);
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:0}));
 assert.equal(record.round,0);
});
test('relay pairing requires both endpoints and rejects unknown capabilities',async()=>{
 assert.equal(iceJoinFields({...join('alpha'),relay_order:['udp'],relay_pairing:'unknown'}),null);
 assert.equal(iceJoinFields({...join('alpha'),relay_pairing:'all-pairs-v1'}),null);
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_order:['udp'],relay_pairing:'all-pairs-v1'}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_order:['tcp']}));
 const record=[...r.ice.records.values()][0];
 assert.equal(record.relay_pairing,'');
 assert.equal(a.messages.at(-1).relay_pairing,undefined);
});
test('relay-only joins reject impossible settings and cannot negotiate legacy',()=>{
 for (const fields of [{relay_only:'true'},{relay_only:true,relay_enabled:false,relay_order:['udp']},{relay_only:true,relay_order:[]},{relay_only:true,transport_profiles:['legacy-ipv6-quic-v2']}])
  assert.equal(iceJoinFields({...join('alpha'),...fields}),null);
 assert.equal(negotiateProfile({...iceJoinFields(join('alpha')),relay_only:true},{}),null);
});
for (const only of [[true,false],[false,true],[true,true]]) {
 test(`relay-only ${only} skips direct on join, retries, rejoin and hibernation`,async()=>{
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  const caps={relay_policy:RELAY_POLICY,relay_pairing:'all-pairs-v1',relay_order:['tcp','udp']};
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps,relay_only:only[0]}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,relay_only:only[1]}));
  const record=[...r.ice.records.values()][0];
  const check=()=>{
   for(const [i,s] of [a,b].entries()) {
    const ready=s.messages.filter(m=>m.type==='transport_ready').at(-1);
    assert.equal(ready.relay_only_policy,'relay-only-v1');assert.equal(ready.relay_required,true);
    assert.equal(ready.relay_only,only[i]);assert.equal(ready.peer_relay_only,only[1-i]);
    assert.notEqual(ready.phase,'direct');assert.notEqual(ready.relay_round,0);
   }
  };
  check();assert.equal(record.round,1);
  for(const round of [2,3,4,0,1,0]) {
   const generation=record.generation;
   const message={type:'transport_restart',transport_id:record.id,expected_generation:generation,relay_round:round};
   await r.webSocketMessage(a,JSON.stringify(message));
   await r.webSocketMessage(b,JSON.stringify(message));
   assert.equal(record.generation,String(BigInt(generation)+1n));check();
  }
  assert.ok(record.retry_after_ms>0);
  const resumed=new Room(f.state,{});
  await resumed.webSocketMessage(a,JSON.stringify({type:'transport_sync'}));check();
  const replacement=f.socket();
  await resumed.webSocketMessage(replacement,JSON.stringify({...join('alpha',['one']),...caps,relay_only:only[0],transport_epoch:'2'}));
  assert.equal(replacement.messages.filter(m=>m.type==='transport_ready').at(-1).relay_round,1);
  assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).relay_round,1);
 });
}
test('relay-only allows a TURN-disabled peer and rejects direct candidates from the constrained endpoint',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const caps={relay_policy:RELAY_POLICY,relay_pairing:'all-pairs-v1',relay_order:['udp']};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps,relay_only:true}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps,relay_enabled:false}));
 const record=[...r.ice.records.values()][0];
 assert.equal(a.messages.at(-1).phase,'relay_udp');assert.equal(b.messages.at(-1).phase,'relay_wait');
 const common={type:'ice_candidate',transport_id:record.id,transport_generation:record.generation};
 const candidate='1 1 udp 2130706431 192.0.2.1 1234 typ host';
 await r.webSocketMessage(a,JSON.stringify({...common,to_peer_id:'beta',candidate}));
 assert.equal(a.messages.at(-1).code,'invalid_ice_candidate');assert.equal(record.ice.alpha,undefined);
 await r.webSocketMessage(b,JSON.stringify({...common,to_peer_id:'alpha',candidate}));
 assert.equal(a.messages.at(-1).candidate,candidate);
 await r.webSocketMessage(a,JSON.stringify({...common,to_peer_id:'beta',candidate:candidate.replace('typ host','typ relay')}));
 assert.equal(b.messages.at(-1).type,'ice_candidate');
});
test('relay-only skips incompatible wait rounds with old round peers and handles named-phase peers',async()=>{
 for(const roundMode of [true,false]) {
  const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
  await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_only:true,relay_policy:RELAY_POLICY,relay_order:['udp']}));
  await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,...(roundMode?{relay_order:['udp','tcp']}: {})}));
  const record=[...r.ice.records.values()][0];
  assert.equal(a.messages.at(-1).phase,'relay_udp');
  const request={type:'transport_restart',transport_id:record.id};
  await r.webSocketMessage(a,JSON.stringify({...request,expected_generation:record.generation,phase:roundMode?'relay_wait':'direct',...(roundMode?{relay_round:2}:{})}));
  assert.equal(a.messages.at(-1).phase,'relay_udp');assert.ok(record.retry_after_ms>0);
 }
});
test('changing relay-only policy resets generation and candidate cache without an epoch change',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket(),caps={relay_order:['udp'],relay_pairing:'all-pairs-v1'};
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),...caps}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),...caps}));
 const record=[...r.ice.records.values()][0];
 record.ice.alpha={candidates:['old']};
 for(const only of [true,false]) {
  const previous=record.generation,replacement=f.socket();
  await r.webSocketMessage(replacement,JSON.stringify({...join('alpha',['one']),...caps,relay_only:only}));
  assert.equal(record.generation,String(BigInt(previous)+1n));assert.deepEqual(record.ice,{});
  assert.equal(record.round,only?1:0);assert.equal(b.messages.at(-1).relay_required,only);
 }
});
test('one persisted pair carries multiple mappings and sorted initiator',async()=>{const{record}=await pair();assert.equal(record.mappings.length,2);assert.equal(record.a,'alpha');assert.equal(record.generation,'1')});
test('same-topology websocket replacement clears blocked transport and cached ICE',async()=>{
 const {r,a,b,record,socket}=await pair();
 record.blocked='peer_identity_mismatch';
 record.ice={alpha:{description:{ufrag:'stale-alpha',pwd:'a'.repeat(22)},candidates:['stale-alpha-candidate'],candidate_bytes:22}};
 const previous=record.generation,replacement=socket();
 await r.webSocketMessage(replacement,JSON.stringify(join('alpha',['one','two'])));
 assert.equal(record.generation,String(BigInt(previous)+1n));
 assert.deepEqual(record.ice,{});assert.equal(record.blocked,null);
 assert.equal(replacement.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,record.generation);
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,record.generation);
 assert.equal(replacement.messages.some(m=>m.type==='ice_description'),false);
 assert.equal(a.closed,true);
});
test('same-runtime websocket replacement resumes a healthy generation',async()=>{
 const {r,a,b,record,socket,state}=await pair();
 record.ice.beta={description:{ufrag:'cached-beta',pwd:'b'.repeat(22)},candidates:['cached-beta-candidate'],candidate_bytes:21};
 const previous=record.generation,replacement=socket();
 await r.webSocketMessage(replacement,JSON.stringify(join('alpha',['one','two'])));
 assert.equal(record.generation,previous);
 assert.equal(record.online,true);
 await r.webSocketMessage(replacement,JSON.stringify({type:'transport_sync'}));
 assert.equal(replacement.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,previous);
 assert.equal(replacement.messages.some(m=>m.type==='ice_description'&&m.ufrag==='cached-beta'),true);
 assert.equal(a.closed,true);
 assert.equal(b.messages.filter(m=>m.type==='peer_state').length,0);
 replacement.closed=true;
 await r.webSocketClose(replacement,1006,'abnormal',false);
 assert.equal(record.online,false);
 const cold=new Room(state,{});
 const resumed=socket();
 await cold.webSocketMessage(resumed,JSON.stringify(join('alpha',['one','two'])));
 assert.equal(record.generation,previous);
 await cold.webSocketMessage(resumed,JSON.stringify({type:'transport_sync'}));
 assert.equal(resumed.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,previous);
});
test('same-topology reconnect after close callback clears blocked transport',async()=>{
 const {r,a,b,record,socket}=await pair();
 record.blocked='peer_identity_mismatch';
 record.ice={alpha:{description:{ufrag:'stale-alpha',pwd:'a'.repeat(22)},candidates:['stale-alpha-candidate'],candidate_bytes:22}};
 const previous=record.generation;
 a.closed=true;await r.webSocketClose(a);
 assert.equal(record.online,false);
 const replacement=socket();
 await r.webSocketMessage(replacement,JSON.stringify(join('alpha',['one','two'])));
 assert.equal(record.generation,String(BigInt(previous)+1n));
 assert.deepEqual(record.ice,{});assert.equal(record.blocked,null);
 assert.equal(replacement.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,record.generation);
 assert.equal(b.messages.filter(m=>m.type==='transport_ready').at(-1).transport_generation,record.generation);
 assert.equal(replacement.messages.some(m=>m.type==='ice_description'),false);
});
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
 assert.deepEqual(validateTurnResponse(payload)[0].urls, ['turn:turn.cloudflare.com:3478?transport=udp','turn:turn.cloudflare.com:3478?transport=tcp','turn:turn.cloudflare.com:80?transport=tcp','turns:turn.cloudflare.com:5349?transport=tcp','turns:turn.cloudflare.com:443?transport=tcp']);
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
test('per-member relay orders translate shared rounds and wrap after the longest list',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_order:['udp']}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,relay_order:['tls_443','udp']}));
 const record=[...r.ice.records.values()][0];
 assert.equal(record.round_mode,true);assert.deepEqual(record.relay_orders,[['udp'],['tls_443','udp']]);
 assert.equal(a.messages.at(-1).relay_round,0);assert.equal(a.messages.at(-1).phase,'direct');
 const restart=async(round,phase)=>r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:round,phase}));
 await restart(1,'relay_udp');
 assert.equal(record.round,1);assert.equal(a.messages.at(-1).phase,'relay_udp');assert.equal(b.messages.at(-1).phase,'relay_tls_443');
 await restart(2,'relay_wait');
 assert.equal(record.round,2);assert.equal(a.messages.at(-1).phase,'relay_wait');assert.equal(b.messages.at(-1).phase,'relay_udp');
 await restart(3,'relay_wait');assert.equal(record.round,0);assert.equal(a.messages.at(-1).phase,'direct');
 await restart(0,'direct');assert.equal(record.round,0);assert.equal(a.messages.at(-1).phase,'direct');
});
test('TURN-disabled peers wait for the enabled peer and two disabled peers skip relay rounds',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket(),all=['udp','tcp_80','tcp','tls_443','tls'];
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_enabled:false,relay_order:all}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,relay_enabled:true,relay_order:['udp']}));
 const record=[...r.ice.records.values()][0];
 assert.deepEqual(record.relay_orders,[[],['udp']]);
 await r.webSocketMessage(b,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:1,phase:'relay_udp'}));
 assert.equal(a.messages.at(-1).relay_round,1);assert.equal(a.messages.at(-1).phase,'relay_wait');
 assert.equal(b.messages.at(-1).relay_round,1);assert.equal(b.messages.at(-1).phase,'relay_udp');

 const both=fixture(),room=new Room(both.state,{}),left=both.socket(),right=both.socket();
 await room.webSocketMessage(left,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_enabled:false,relay_order:all}));
 await room.webSocketMessage(right,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,relay_enabled:false,relay_order:all}));
 const directOnly=[...room.ice.records.values()][0];
 assert.deepEqual(directOnly.relay_orders,[[],[]]);assert.equal(directOnly.round,0);
 await room.webSocketMessage(left,JSON.stringify({type:'transport_restart',transport_id:directOnly.id,expected_generation:directOnly.generation,relay_round:0,phase:'direct'}));
 assert.equal(directOnly.round,0);assert.equal(left.messages.at(-1).phase,'direct');
});
test('round overflow wraps after the maximum custom order',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 const all=['udp','tcp_80','tcp','tls_443','tls'];
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_order:all}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,relay_order:all}));
 const record=[...r.ice.records.values()][0];
 const phases=['relay_udp','relay_tcp_80','relay_tcp','relay_tls_443','relay_tls'];
 for(let round=1;round<=phases.length;round++)
  await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:round,phase:phases[round-1]}));
 await r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:record.id,expected_generation:record.generation,relay_round:6,phase:'relay_wait'}));
 assert.equal(record.round,0);assert.equal(a.messages.at(-1).phase,'direct');
});
test('relay order joins reject unknown, duplicate, and oversized lists',()=>{
 for(const relay_order of [['direct'],['udp','udp'],['udp','tcp_80','tcp','tls_443','tls','udp']])
  assert.equal(iceJoinFields({...join('alpha'),relay_order}),null);
 assert.deepEqual(iceJoinFields({...join('alpha'),relay_order:['tls','udp']}).relay_order,['tls','udp']);
 assert.deepEqual(iceJoinFields({...join('alpha'),relay_enabled:false,relay_order:['udp']}).relay_order,[]);
 assert.equal(iceJoinFields({...join('alpha'),relay_enabled:false}),null);
});
test('changing a member relay order resets the shared generation',async()=>{
 const f=fixture(),r=new Room(f.state,{}),a=f.socket(),b=f.socket();
 await r.webSocketMessage(a,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_order:['udp']}));
 await r.webSocketMessage(b,JSON.stringify({...join('beta',[],['one']),relay_policy:RELAY_POLICY,relay_order:['udp']}));
 const record=[...r.ice.records.values()][0];
 const replacement=f.socket();
 await r.webSocketMessage(replacement,JSON.stringify({...join('alpha',['one']),relay_policy:RELAY_POLICY,relay_order:['tls']}));
 assert.equal(record.generation,'2');assert.equal(record.round,0);assert.deepEqual(record.relay_orders,[['tls'],['udp']]);
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
test('unreferenced modern members release capacity after disconnect and cold reconstruction',async()=>{
 const f=await modernFixture();
 for(let i=0;i<129;i++) {
  const ws=f.socket(),member=modernJoin('device-'+i);
  await f.r.webSocketMessage(ws,JSON.stringify(member));
  assert.equal(ws.messages.some(m=>m.type==='joined'),true,member.device_name);
  if(i===0) await f.r.webSocketMessage(ws,JSON.stringify({type:'room_graph',room_frames:[await f.nodeFrame(member)]}));
  ws.closed=true;await f.r.webSocketClose(ws,1000,'',true);
  assert.equal(f.r.registered.size,0);
  assert.equal(f.values.has('member:'+member.device_name),false);
 }
 assert.equal(f.r.control.nodes.size,0);
 assert.deepEqual(f.values.get('room_nodes'),[]);
 // Upgrade/reconstruction must reclaim declarations left by the old code too.
 for(let i=0;i<128;i++) await f.state.storage.put('member:old-'+i,modernJoin('old-'+i));
 const restored=f.newRoom(),next=f.socket();
 await restored.webSocketMessage(next,JSON.stringify(modernJoin('new-device')));
 assert.equal(next.messages.some(m=>m.type==='joined'),true);
 assert.deepEqual([...restored.registered.keys()],['new-device']);
 assert.deepEqual([...f.values.keys()].filter(k=>k.startsWith('member:')),['member:new-device']);
 assert.equal(f.values.get('token'),'room-secret');
 assert.ok(f.values.get('room_control_key'));
});
test('member cleanup cannot delete a same-name join while its storage write is pending',async()=>{
 const f=await modernFixture(),member=modernJoin('alpha'),ws=f.socket();
 f.r.registered.set('alpha',{member,ws:null});
 await f.state.storage.put('member:alpha',member);
 let started,release;
 const writing=new Promise(resolve=>{started=resolve}),gate=new Promise(resolve=>{release=resolve});
 const put=f.state.storage.put;
 f.state.storage.put=async(k,v)=>{await put(k,v);if(k==='member:alpha'){started();await gate}};
 const joining=f.r.webSocketMessage(ws,JSON.stringify(member));
 await writing;
 try { await f.r.reconcile(); } finally { release();await joining; }
 assert.equal(ws.messages.some(m=>m.type==='joined'),true);
 assert.equal(f.values.get('member:alpha').runtime_id,member.runtime_id);
 const restored=f.newRoom();await restored.loadState();
 assert.equal(restored.registered.get('alpha').ws,ws);
});
test('modern transport retention starts at disconnect after long uptime and preserves resume',async t=>{
 for(const cold of [false,true]) await t.test(cold?'cold alarm':'warm alarm',async t=>{
  const f=await modernFixture(),a=f.socket(),b=f.socket();
  const left=modernJoin('alpha',['one']),right=modernJoin('beta',[],['one']);
  await f.r.webSocketMessage(a,JSON.stringify(left));await f.r.webSocketMessage(b,JSON.stringify(right));
  const original=[...f.r.ice.records.values()][0];
  await f.r.webSocketMessage(a,JSON.stringify({type:'transport_restart',transport_id:original.id,expected_generation:original.generation,phase:'direct',reason:'manual_renomination'}));
  const generation=original.generation;
  assert.notEqual(generation,'1');
  let now=Date.now()+25*60*60*1000;t.mock.method(Date,'now',()=>now);
  a.closed=true;await f.r.webSocketClose(a,1006,'',false);
  const expires=now+24*60*60*1000;
  now+=1000;b.closed=true;await f.r.webSocketClose(b,1006,'',false);
  assert.equal(original.expires_at,expires);
  assert.equal(await f.state.storage.getAlarm(),expires);
  const writes=f.counts.puts;await f.r.reconcile();assert.equal(f.counts.puts,writes);
  assert.equal(f.r.registered.size,2);
  const restored=f.newRoom();await restored.alarm();
  assert.equal([...restored.ice.records.values()][0].id,original.id);
  const nextA=f.socket(),nextB=f.socket();
  await restored.webSocketMessage(nextA,JSON.stringify(left));await restored.webSocketMessage(nextB,JSON.stringify(right));
  const resumed=[...restored.ice.records.values()][0];
  assert.equal(resumed.id,original.id);assert.equal(resumed.generation,generation);
  assert.equal(resumed.online,true);assert.equal(await f.state.storage.getAlarm(),null);
  nextA.closed=true;await restored.webSocketClose(nextA,1000,'',true);
  nextB.closed=true;await restored.webSocketClose(nextB,1000,'',true);
  assert.equal(resumed.expires_at,now+24*60*60*1000);
  now=resumed.expires_at+1;
  const cleaning=cold?f.newRoom():restored;await cleaning.alarm();
  assert.equal(cleaning.ice.records.size,0);assert.equal(cleaning.registered.size,0);
  assert.equal([...f.values.keys()].some(k=>k.startsWith('transport:')||k.startsWith('member:')),false);
  assert.equal(await f.state.storage.getAlarm(),null);
  assert.equal(f.values.get('token'),'room-secret');assert.ok(f.values.get('room_control_key'));
 });
});
test('cleanup retains delegated members and control edges after long uptime and hibernation',async t=>{
 for(const controlOnly of [false,true]) await t.test(controlOnly?'control edge':'business edge',async t=>{
  const f=await modernFixture(),a=f.socket(),b=f.socket();
  const left=modernJoin('alpha',['one']),right=modernJoin('beta',[],['one']);
  await f.r.webSocketMessage(a,JSON.stringify(left));await f.r.webSocketMessage(b,JSON.stringify(right));
  const record=[...f.r.ice.records.values()][0];
  for(const ws of [a,b]) await f.r.webSocketMessage(ws,JSON.stringify({type:'transport_established',transport_id:record.id,transport_generation:record.generation,path_type:'direct'}));
  const frames=await Promise.all([[left,right],[right,left]].map(async([own,peer])=>f.nodeFrame(own,[{peer_id:peer.device_name,runtime_id:peer.runtime_id,transport_id:record.id,generation:record.generation}])));
  await f.r.webSocketMessage(a,JSON.stringify({type:'room_graph',room_frames:frames}));
  await f.r.webSocketMessage(b,JSON.stringify({type:'room_pause',request_id:'pause'}));
  assert.equal(b.messages.some(m=>m.type==='room_pause_rejected'),false);
  assert.equal(b.messages.some(m=>m.type==='room_paused'),false);
  assert.equal(b.closed,false);
  assert.deepEqual(f.r.control.snapshot().groups[0].signaling,['alpha','beta']);
  await f.r.webSocketClose(b,1006,'network disconnected',false);
  if(controlOnly) await f.r.webSocketMessage(a,JSON.stringify(memberUpdate({...left,provide:[]},1)));
  assert.equal(record.room_control,controlOnly);
  const now=Date.now()+25*60*60*1000;t.mock.method(Date,'now',()=>now);
  await f.r.alarm();
  const restored=f.newRoom();await restored.alarm();
  assert.equal(restored.members.get('beta').ws.proxy,true);
  assert.equal(restored.registered.get('beta').ws,null);
  assert.equal(restored.control.nodes.size,2);
  assert.equal([...restored.ice.records.values()][0].id,record.id);
  assert.equal([...restored.ice.records.values()][0].online,true);
  assert.equal(await f.state.storage.getAlarm(),null);
 });
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
