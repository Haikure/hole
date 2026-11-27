// One active path and at most one candidate per pair. The active route keeps
// its original generation and grant until both endpoints install a successor.
const fields=['generation','phase','round','direct_stage','hole','peer_relay','established'];
const snapshot=r=>Object.fromEntries(fields.map(k=>[k,structuredClone(r[k]??null)]));
const activeRecord=r=>({...r,...r.upgrade.active,upgrade:null});
const relayPaths=new Set(['relay','hole','peer_relay']);
const enabled=r=>r.path_upgrade && r.local_management && !r.room_control && !r.relay_required && !r.blocked && r.online;
const activeRelay=r=>[r.a,r.b].every(n=>r.established?.[n]?.generation===r.generation && relayPaths.has(r.established[n].path_type));

export function grantUpgradeActive(ice,r) {
  if (!r.upgrade) return;
  const active=activeRecord(r);
  ice.grantHole(active);ice.grantPeerRelay(active);
}

export async function clearUpgrade(ice,r) {
  if (!r.upgrade) return;
  const active=activeRecord(r);
  await ice.revokeHole(active);ice.revokePeerRelay(active);
  r.upgrade=null;
}

async function publish(ice,r) {
  await ice.persist(r);
  ice.notify(r,r.a);ice.notify(r,r.b);
}

async function candidate(ice,r,target) {
  // The caller has already verified a better path exists or is due to probe.
  ice.revokePeerRelay(r);
  r.generation=String(BigInt(r.generation)+1n);
  r.ice={};r.established={};r.hole=null;r.peer_relay=null;
  r.phase='direct';r.round=0;r.direct_stage='ice';r.retry_after_ms=0;
  Object.assign(r.upgrade,{target,idle:false,prepared:{},committed:false});
  if(target==='peer_relay') {
    // Preserve the normal fallback round for validation and recovery.
    r.round=r.upgrade.active.round??1;
    if(!ice.preparePeerRelay(r)) {r.upgrade.idle=true;r.round=0;}
  }
  await publish(ice,r);
}

export async function startUpgrade(ice,r,peerOnly=false) {
  if(!enabled(r) || r.upgrade?.committed) return false;
  if(!r.upgrade && !activeRelay(r)) return false;
  const active=r.upgrade?activeRecord(r):r;
  const alreadyPeer=[r.a,r.b].some(n=>active.established?.[n]?.path_type==='peer_relay');
  const peer=!alreadyPeer && Date.now()>=(r.peer_upgrade_after??0) && ice.peerRelayCandidate(r);
  if(peerOnly && (!peer || r.upgrade && !r.upgrade.idle && r.upgrade.target==='peer_relay')) return false;
  if(!peerOnly && r.upgrade && !r.upgrade.idle) return false;
  if(!r.upgrade) r.upgrade={active:snapshot(r)};
  // Moving the old grant to upgrade.active must not revoke it.
  if(r.upgrade.active.generation===r.generation) {r.hole=null;r.peer_relay=null;}
  await candidate(ice,r,peer?'peer_relay':'direct');
  return true;
}

export async function scanPeerUpgrades(ice) {
  for(const r of ice.records.values()) await startUpgrade(ice,r,true);
}

export async function handleUpgrade(ice,r,name,m) {
  if(m.type==='transport_upgrade_prepared') {
    const u=r.upgrade;
    if(!enabled(r) || !u || u.idle || m.transport_generation!==r.generation || m.path_type!==u.target) return true;
    u.prepared[name]=true;
    if([r.a,r.b].every(n=>u.prepared[n])) u.committed=true;
    await ice.persist(r);
    if(u.committed) {ice.notify(r,r.a);ice.notify(r,r.b);}
    return true;
  }
  if(m.type!=='transport_restart') return false;
  if(m.reason==='auto_upgrade') {
    if(enabled(r) && name===r.a) {
      const started=await startUpgrade(ice,r);
      if(!started) ice.notify(r,name,true);
    }
    return true;
  }
  if(!r.upgrade) return ['upgrade_failed','upgrade_path_lost'].includes(m.reason);
  if(m.reason==='turn_refresh' && !r.upgrade.active.hole && !r.upgrade.active.peer_relay) {
    // The retained TURN allocation needs renewal. Resume its normal stage;
    // the endpoint still keeps the old QUIC until the replacement is ready.
    const generation=r.generation,active=r.upgrade.active;
    ice.revokePeerRelay(r);
    Object.assign(r,active,{generation,upgrade:null,ice:{}});
    return false;
  }
  if(m.reason==='upgrade_path_lost' || m.reason==='path_lost' || m.reason==='retry' || r.upgrade.committed) {
    // The active route really failed, or a committed candidate was lost.
    // Return to normal recovery with a fresh generation, never revive a grant.
    await clearUpgrade(ice,r);ice.revokePeerRelay(r);
    r.generation=String(BigInt(r.generation)+1n);r.ice={};r.established={};
    r.round=0;r.phase='direct';r.direct_stage='ice';r.retry_after_ms=0;
    await publish(ice,r);return true;
  }
  if(m.reason==='manual_renomination') {await candidate(ice,r,'direct');return true;}
  if(r.upgrade.idle) {ice.notify(r,name,true);return true;}
  if(m.reason==='ice_failed' && r.upgrade.target==='direct' && r.direct_order==='ice-first-v1' && r.direct_stage==='ice') {
    r.generation=String(BigInt(r.generation)+1n);r.ice={};r.upgrade.prepared={};r.direct_stage='punch';
    await publish(ice,r);return true;
  }
  // Coalesce a late ICE failure after the other endpoint began punching.
  if(m.reason==='ice_failed' && r.direct_stage==='punch') {ice.notify(r,name,true);return true;}
  if(r.upgrade.target==='peer_relay') r.peer_upgrade_after=Date.now()+30_000;
  ice.revokePeerRelay(r);
  r.upgrade.idle=true;r.upgrade.prepared={};r.ice={};r.round=0;r.phase='direct';r.direct_stage='ice';
  await publish(ice,r);return true;
}

export async function finishUpgrade(ice,r) {
  if(!r.upgrade?.committed || !ice.established(r)) return;
  await clearUpgrade(ice,r);
  await ice.persist(r);
  ice.notify(r,r.a);ice.notify(r,r.b);
}
