(function (root) {
    'use strict';

    function escapeText(value) {
        return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
            return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c];
        });
    }

    function summaryText(row) {
        var s = row.latest_execution_summary;
        if (!s || !s.total_count) return '未配置执行节点';
        var parts = [];
        [['success_count', '成功'], ['failed_count', '失败'], ['timeout_count', '超时'], ['no_record_count', '无记录']].forEach(function (item) {
            var count = Number(s[item[0]]) || 0;
            if (count > 0) parts.push(item[1] + ' ' + count);
        });
        return parts.join(' / ');
    }

    function bind(table, form, $, laydate) {
        var element = $('#task-search');
        function reload() {
            var where = {};
            element.serializeArray().forEach(function (item) { where[item.name] = item.value; });
            table.reload('listReload', {where: where, page: {curr: 1}});
        }
        element.find('.task-search-date').each(function () {
            laydate.render({elem: this, format: 'yyyy-MM-dd'});
        });
        $('#task-search-submit').on('click', reload);
        $('#task-search-reset').on('click', function () {
            element[0].reset();
            form.render('select');
            reload();
        });
        element.on('keydown', 'input', function (event) {
            // Searchable LayUI selects use an input of their own; Enter there
            // selects a person rather than prematurely submitting the form.
            if (event.keyCode === 13 && !$(this).closest('.layui-form-select').length) {
                event.preventDefault();
                reload();
            }
        });
        $('#task-search-owned').on('click', function () {
            $('#owner_id').val(String(element.data('user-id')));
            $('#create_id').val('');
            form.render('select');
            reload();
        });
        $('#task-search-created').on('click', function () {
            $('#create_id').val(String(element.data('user-id')));
            $('#owner_id').val('');
            form.render('select');
            reload();
        });
        form.render('select');
        return reload;
    }

    root.PPTaskSearch = {bind: bind, summaryText: summaryText, escapeText: escapeText};
}(typeof window !== 'undefined' ? window : globalThis));
