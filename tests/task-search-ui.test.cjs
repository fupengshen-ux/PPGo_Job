const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

const context = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/admin/js/task-search.js'), 'utf8'), context);
const api = context.PPTaskSearch;

test('summary keeps mixed outcomes and distinguishes no records from no nodes', () => {
    assert.equal(api.summaryText({latest_execution_summary: {total_count: 4, success_count: 1, failed_count: 1, timeout_count: 1, no_record_count: 1}}), '成功 1 / 失败 1 / 超时 1 / 无记录 1');
    assert.equal(api.summaryText({latest_execution_summary: {total_count: 1, no_record_count: 1}}), '无记录 1');
    assert.equal(api.summaryText({latest_execution_summary: {total_count: 0}}), '未配置执行节点');
    assert.equal(api.escapeText('<img src="x">&'), '&lt;img src=&quot;x&quot;&gt;&amp;');
});

test('query, reset, Enter and personal shortcuts reset pagination without stale filters', () => {
    const defaults = {task_name: '', task_id: '', owner_id: '', create_id: '', group_id: '0', task_status: '', server_id: '', latest_result: 'all', created_from: '', created_to: ''};
    let values = {...defaults, task_name: '商品', owner_id: '1', create_id: '3', latest_result: 'failed'};
    const handlers = {}, reloads = [];
    const form = {
        0: {reset() { values = {...defaults}; }},
        serializeArray() { return Object.entries(values).map(([name, value]) => ({name, value})); },
        data() { return 2; },
        find() { return {each() {}}; },
        on(event, selector, handler) { handlers[event] = handler; }
    };
    const $ = selector => {
        if (selector === '#task-search') return form;
        if (typeof selector !== 'string') return {closest() { return {length: selector.isSelect ? 1 : 0}; }};
        return {
            on(event, handler) { handlers[selector] = handler; },
            val(value) { if (value === undefined) return values[selector.slice(1)]; values[selector.slice(1)] = value; }
        };
    };
    api.bind({reload(id, options) { assert.equal(id, 'listReload'); reloads.push(options); }}, {render() {}}, $, {render() {}});
    handlers['#task-search-submit']();
    assert.equal(reloads.at(-1).page.curr, 1);
    assert.equal(reloads.at(-1).where.latest_result, 'failed');
    handlers['#task-search-owned']();
    assert.equal(values.owner_id, '2');
    assert.equal(values.create_id, '');
    assert.equal(values.task_name, '商品');
    handlers['#task-search-created']();
    assert.equal(values.create_id, '2');
    assert.equal(values.owner_id, '');
    handlers['#task-search-reset']();
    assert.deepEqual(JSON.parse(JSON.stringify(reloads.at(-1).where)), defaults);
    const before = reloads.length;
    let prevented = false;
    handlers.keydown.call({isSelect: false}, {keyCode: 13, preventDefault() { prevented = true; }});
    assert.equal(reloads.length, before + 1);
    assert.ok(prevented);
    handlers.keydown.call({isSelect: true}, {keyCode: 13});
    assert.equal(reloads.length, before + 1);
});
