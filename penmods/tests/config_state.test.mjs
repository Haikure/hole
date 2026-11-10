import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import assert from 'node:assert/strict';

const source = readFileSync(new URL('../Main.qml', import.meta.url), 'utf8');
function form() {
    const context = vm.createContext({ saveState: '已保存，正在应用' });
    context.page = context;
    for (const name of ['finishConfigSave', 'handleCoreEvent']) {
        const fn = source.match(new RegExp(`    function ${name}\\([^]*?\\n    }`));
        assert.ok(fn, name);
        vm.runInContext(fn[0], context);
    }
    return context;
}

test('live completion and unchanged configuration finish the save indicator', () => {
    for (const state of ['applied', 'unchanged']) {
        const page = form();
        page.handleCoreEvent(JSON.stringify({ params: { kind: 'config', state } }));
        assert.equal(page.saveState, '已保存，已应用');
    }
});

test('staging, failure and snapshot recovery have distinct save results', () => {
    const page = form();
    page.finishConfigSave('staged', '');
    assert.equal(page.saveState, '已保存，待启动后应用');
    page.saveState = '已保存，正在应用';
    page.finishConfigSave('failed', '映射重复');
    assert.equal(page.saveState, '已保存，应用失败：映射重复');
    // A later running snapshot must preserve the rejected-edit message.
    page.finishConfigSave('applied', '');
    assert.equal(page.saveState, '已保存，应用失败：映射重复');
    page.saveState = '已保存，正在应用';
    page.finishConfigSave('applied', '');
    assert.equal(page.saveState, '已保存，已应用');
});

test('unrelated events and older completion do not overwrite newer form feedback', () => {
    const page = form();
    page.handleCoreEvent('{invalid');
    page.handleCoreEvent(JSON.stringify({ params: { kind: 'mapping', state: 'applied' } }));
    page.handleCoreEvent(JSON.stringify({ params: { kind: 'config', state: 'applying' } }));
    assert.equal(page.saveState, '已保存，正在应用');
    page.saveState = '已保存，暂未应用：缺少服务端口';
    page.handleCoreEvent(JSON.stringify({ params: { kind: 'config', state: 'applied' } }));
    assert.equal(page.saveState, '已保存，暂未应用：缺少服务端口');
});
