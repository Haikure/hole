import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

// 执行 Main.qml 中真实的配置读写函数；仅替换宿主插件、ListModel 和定时器。
const source = readFileSync(new URL('../Main.qml', import.meta.url), 'utf8');
const properties = [...source.matchAll(/^    (?:readonly )?property (?:string|bool|int|var) (\w+): (.+)$/gm)]
    .map(([, name, value]) => `${name} = ${value};`).join('\n');
const functions = [...source.matchAll(/^    function \w+\([^]*?^    }/gm)]
    .map(([body]) => body).join('\n');

function listModel() {
    const rows = [];
    return {
        get count() { return rows.length; },
        clear() { rows.length = 0; },
        append(row) { rows.push(row); },
        get(index) { return rows[index]; },
    };
}

function createPage(saved = {}) {
    const plugin = {
        serverUrl: 'wss://signal.example/ws',
        configJson: '{}',
        mappingStateJson: '',
        turnStateJson: '',
        ...saved,
        saveConfig() { return true; },
    };
    const page = vm.createContext({
        holePlugin: plugin,
        console,
        provides: listModel(),
        consumes: listModel(),
        autosave: { restart() {} },
    });
    page.page = page;
    vm.runInContext(`${properties}\n${functions}`, page);
    page.refreshStatus = () => {};
    page.readConfig();
    return page;
}

function reopen(page) {
    // 模拟销毁页面并从插件保存的 JSON 重新加载，不沿用旧表单对象。
    return createPage(JSON.parse(JSON.stringify(page.holePlugin)));
}

const manualTurn = {
    mode: 'manual',
    ttl: '2h',
    urls: ['turn:relay.example:3478', 'turns:relay.example:5349'],
    username: 'test-user',
    credential: 'test-credential',
    order: ['tls', 'udp'],
};

function enterManualTurn(page) {
    page.turnMode = manualTurn.mode;
    page.turnTtl = manualTurn.ttl;
    page.turnUrls = manualTurn.urls.join(', ');
    page.turnUsername = manualTurn.username;
    page.turnCredential = manualTurn.credential;
    page.turnOrder = manualTurn.order.join(', ');
    page.syncConfig();
}

function runtimeTurn(page) {
    return JSON.parse(page.holePlugin.configJson).turn;
}

function assertManualFields(page) {
    assert.equal(page.turnUrls, manualTurn.urls.join(', '));
    assert.equal(page.turnUsername, manualTurn.username);
    assert.equal(page.turnCredential, manualTurn.credential);
    assert.equal(page.turnTtl, manualTurn.ttl);
    assert.equal(page.turnOrder, manualTurn.order.join(', '));
}

for (const mode of ['worker', 'off', 'ipv6']) {
    test(`手动 TURN → ${mode} → 重开页面 → 手动，保留配置但隔离运行凭据`, () => {
        let page = createPage();
        enterManualTurn(page);
        if (mode === 'ipv6') page.preferred = 'ipv6';
        else page.turnMode = mode;
        page.syncConfig();
        page.saveNow();

        const inactive = runtimeTurn(page);
        assert.equal(inactive.mode, mode === 'ipv6' ? 'off' : mode);
        assert.deepEqual(inactive.urls, []);
        assert.equal(inactive.username, '');
        assert.equal(inactive.credential, '');
        assert.equal(JSON.parse(page.holePlugin.configJson).turn_state, undefined);
        assert.equal(JSON.parse(page.holePlugin.turnStateJson).credential, manualTurn.credential);

        page = reopen(page);
        assertManualFields(page);
        assert.equal(page.turnMode, mode === 'ipv6' ? 'manual' : mode);
        assert.equal(page.preferred, mode === 'ipv6' ? 'ipv6' : 'ice');
        // 在非手动模式再次保存，不能用运行配置覆盖保留的编辑状态。
        page.deviceName = 'updated-pen';
        page.syncConfig();
        page = reopen(page);
        assertManualFields(page);
        page.preferred = 'ice';
        page.turnMode = 'manual';
        page.syncConfig();
        assert.deepEqual(runtimeTurn(page), manualTurn);
    });
}

test('旧配置没有 turn_state 时从运行配置读取并保留手动 TURN', () => {
    let page = createPage({ configJson: JSON.stringify({ turn: manualTurn }) });
    assertManualFields(page);
    page.turnMode = 'worker';
    page.syncConfig();
    page = reopen(page);
    assertManualFields(page);
    assert.equal(page.turnMode, 'worker');
});

test('未填写完整的手动 TURN 切到协调服务后仍保留草稿', () => {
    let page = createPage();
    page.turnMode = 'manual';
    page.turnUrls = 'turn:unfinished';
    page.turnUsername = 'draft-user';
    page.turnCredential = '';
    page.syncConfig();
    page.turnMode = 'worker';
    page.syncConfig();
    page = reopen(page);
    assert.equal(page.turnUrls, 'turn:unfinished');
    assert.equal(page.turnUsername, 'draft-user');
    assert.equal(page.turnCredential, '');
    assert.deepEqual(runtimeTurn(page).urls, []);
});

test('主动清空的手动凭据不会在重开页面时恢复为旧值', () => {
    let page = createPage();
    enterManualTurn(page);
    page.turnUrls = '';
    page.turnUsername = '';
    page.turnCredential = '';
    page.turnMode = 'worker';
    page.syncConfig();
    page = reopen(page);
    assert.equal(page.turnUrls, '');
    assert.equal(page.turnUsername, '');
    assert.equal(page.turnCredential, '');
});

test('非对象的 TURN 编辑状态回退到旧运行配置', () => {
    for (const turnStateJson of ['null', '[]', '"invalid"']) {
        const page = createPage({ configJson: JSON.stringify({ turn: manualTurn }), turnStateJson });
        assertManualFields(page);
    }
});
