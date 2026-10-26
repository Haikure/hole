import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../Main.qml', import.meta.url), 'utf8');
const functions = [...source.matchAll(/^    function \w+\([^]*?^    }/gm)].map(([body]) => body).join('\n');
const page = vm.createContext({});
page.page = page;
vm.runInContext(functions, page);
const fixture = JSON.parse(readFileSync(new URL('../../desktop/gui/tests/fixtures/fixed_snapshot.json', import.meta.url), 'utf8'));
function lines(peer) {
    const result = [];
    page.appendPeerSnapshotLines(result, peer);
    return Object.fromEntries(result.map(row => [row.label.trim(), row.text]));
}

test('固定快照保留真实状态、入口、方向和实际 IPv6 地址', () => {
    const peers = fixture.peer_transports;
    for (const peer of peers) assert.match(lines(peer)['路径'], /^固定入口直连/);
    const active = lines(peers[0]);
    assert.equal(active['路径'], '固定入口直连 · IPv6');
    assert.equal(active['公布入口'], 'gateway.example:40000');
    assert.equal(active['监听方'], 'fixed-ipv6');
    assert.equal(active['拨号方'], 'desktop');
    assert.equal(active['对端连接'], '固定入口直连 · [2001:db8:7:8:9:a:b:c]:40000');
    assert.equal(active['连接阶段'], '连接固定入口');
    assert.equal(lines(peers[1])['通道 · fixed-connecting'], '连接中');
    assert.equal(lines(peers[2])['监听方'], 'desktop');
    assert.equal(lines(peers[2])['路径'], '固定入口直连 · IPv4');
    assert.equal(lines(peers[2])['通道 · fixed-retrying'], '重新连接中');
    assert.match(lines(peers[2])['连接提示'], /仅重试固定入口/);
    assert.match(lines(peers[3])['连接提示'], /仅中继/);
});

test('缺失字段不推测，固定通道不显示旧的 TURN 数据', () => {
    const missing = lines(fixture.peer_transports[3]);
    for (const key of ['公布入口', '监听方', '拨号方', '本机连接', '对端连接']) assert.equal(missing[key], undefined);
    const fixed = lines({...fixture.peer_transports[0], relay_order: ['tls'], peer_relay_order: ['udp'], relay_order_fallback: true});
    assert.ok(!Object.keys(fixed).some(key => /TURN|中继|候选|凭据/.test(key)));
    assert.ok(fixed['线路流量']);
    assert.ok(fixed['活动通道']);
});

test('ICE 直连、中继和阶段显示保持兼容', () => {
    assert.equal(page.peerPathText({path_type: 'direct', address_family: 'IPv6'}), '设备直连 · IPv6');
    const relay = lines({path_type: 'relay', local_type: 'relay', local_relay_protocol: 'tcp', remote_type: 'host', phase: 'relay_tcp_80'});
    assert.equal(relay['本机连接'], 'TCP 中继');
    assert.equal(relay['对端连接'], '直连');
    assert.equal(relay['探测阶段'], '当前阶段：尝试 TCP 中继 · 80 端口');
    assert.equal(page.isFixedPeer({phase: 'direct'}), false);
});
