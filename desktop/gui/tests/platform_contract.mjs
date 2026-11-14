import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

const source=readFileSync(new URL('../../../penmods/Main.qml',import.meta.url),'utf8');
for(const fixture of JSON.parse(readFileSync(0,'utf8'))){
    const c=fixture.config.connection,s=fixture.secrets;
    const page={initialized:true,defaultStun:'stun:stun.cloudflare.com:3478',
        serverUrl:c.server_url,room:c.room,password:s.password,token:s.token,deviceName:c.device_name,sessionTimeout:c.session_timeout,
        preferred:c.connection_mode==='legacy'?'ipv6':'ice',allowLegacy:c.connection_mode==='auto',allowInsecureSignal:c.allow_insecure_signal,
        candidateAddresses:c.candidate_addresses.join('\n'),stunUrls:c.ice.stun_urls.join('\n'),natStunUrls:c.ice.nat_stun_urls,portMapping:c.ice.port_mapping,
        directProbeTimeout:c.ice.direct_probe_timeout,gatherTimeout:c.ice.gather_timeout,connectivityTimeout:c.ice.connectivity_timeout,retryMaxDelay:c.ice.retry_max_delay,
        interfaceAllowlist:c.ice.interface_allowlist.join('\n'),includeLoopback:c.ice.include_loopback,relayOnly:c.ice.relay_only,
        turnMode:c.turn.mode,turnTtl:c.turn.ttl,turnUrls:c.turn.urls.join('\n'),turnUsername:c.turn.username,turnCredential:s.turn_credential,turnOrder:c.turn.order.join(','),
        relayDevice:c.relay.device,relayOrderExplicit:c.relay.order!==null,relayOrder:(c.relay.order||[]).join(','),relayServeUnsupported:false,
        holePlugin:{},autosave:{restart(){}},mappingState(){return {};},provides:[],consumes:[],collectMappings(){return [];}};
    const context=vm.createContext(page);context.page=context;
    for(const name of ['asString','parseList','parseRelayOrder','syncConfig']){
        const fn=source.match(new RegExp(`    function ${name}\\([^]*?\\n    }`));assert.ok(fn,name);vm.runInContext(fn[0],context);
    }
    context.syncConfig();
    const actual=JSON.parse(context.holePlugin.configJson),expected=structuredClone(fixture.request.config);
    delete expected.candidate_interfaces;delete expected.voice;delete expected.relay.serve;
    assert.deepEqual(actual,expected,`${c.connection_mode}/${c.turn.mode}/${JSON.stringify(c.ice.nat_stun_urls)}`);
    assert.equal(context.holePlugin.serverUrl,fixture.request.server_url);
}
console.log('PASS: 21 active transport requests match the actual PenMods serializer');
