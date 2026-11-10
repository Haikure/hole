import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

// Execute the actual QML JavaScript formatters; no Qt/device dependencies.
const source = readFileSync(new URL('../Main.qml', import.meta.url), 'utf8');
const context = vm.createContext({ asString: value => String(value ?? '') });
for (const name of ['sendLossText', 'quicLossText', 'bandwidthText']) {
    const fn = source.match(new RegExp(`    function ${name}\\([^]*?\\n    }`));
    assert.ok(fn, name);
    vm.runInContext(fn[0], context);
}

test('QUIC summaries preserve weighted rate and core simultaneous peak', () => {
    assert.equal(context.sendLossText({ window_seconds: 60, samples: '1000', lost_packets: '19', rate_percent: 1.9 }), '1.90%');
    assert.match(context.sendLossText({ window_seconds: 60, samples: '0', lost_packets: '0', rate_percent: null }), /暂无样本/);
    assert.equal(context.sendLossText(undefined), '—');
    assert.equal(context.quicLossText({ send_loss: { rate_percent: 0 } }), '合计 暂无反馈 · 上行 0.00% · 下行 暂无反馈');
    const bandwidth = {
        send: { peak_bps: 8e6, average_bps: 4e6 },
        receive: { peak_bps: 16e6, average_bps: 8e6 },
        total: { peak_bps: 16e6, average_bps: 12e6 },
    };
    assert.equal(context.bandwidthText(bandwidth, true), '合计 16 Mbps · 上行 8 Mbps · 下行 16 Mbps');
    assert.equal(context.bandwidthText(bandwidth, false), '合计 12 Mbps · 上行 4 Mbps · 下行 8 Mbps');
    assert.equal(context.bandwidthText(undefined, true), '—');
    bandwidth.total.peak_bps = -1;
    assert.equal(context.bandwidthText(bandwidth, true), '—');
    bandwidth.total.peak_bps = 0;
    bandwidth.send.peak_bps = 1.25e6;
    bandwidth.receive.peak_bps = 10e6;
    assert.equal(context.bandwidthText(bandwidth, true), '合计 0 Mbps · 上行 1.25 Mbps · 下行 10 Mbps');
});
