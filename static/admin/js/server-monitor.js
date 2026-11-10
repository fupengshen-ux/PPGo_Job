(function (root) {
    'use strict';
    const names = {ok:'正常',partial:'部分指标缺失',failed:'采集失败',unsupported:'Agent 需升级或系统不支持',unmonitored:'未接入监控',waiting:'等待采集',stale:'数据过期'};
    function escapeText(v) { return String(v == null ? '' : v).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
    function numeric(v) { return typeof v === 'number' && Number.isFinite(v); }
    function sortable(row, key) {
        if (row.status === 'stale') return false;
        if (key === 'assigned' || key === 'enabled') return numeric(row[key]);
        if (key === 'memory_available' && !numeric(row.memory)) return false;
        return (row.status === 'ok' || row.status === 'partial') && numeric(row[key]);
    }
    function compare(a,b,key,desc) {
        const av=sortable(a,key),bv=sortable(b,key);
        if (av !== bv) return av ? -1 : 1;
        return av ? ((a[key]-b[key])*(desc ? -1 : 1) || a.server_id-b.server_id) : a.server_id-b.server_id;
    }
    function historyPoints(rows,key) {
        const out=[];let last=0;
        rows.forEach(r=>{
            if (last && r.collected_at-last>90) out.push([(last+60)*1000,null]);
            out.push([r.collected_at*1000,numeric(r[key]) ? r[key] : null]);last=r.collected_at;
        });return out;
    }
    const historyMetrics = [
        {key:'cpu',name:'CPU 使用率',color:'#5470C6',axis:0,unit:'%'},
        {key:'memory',name:'内存使用率',color:'#3BA272',axis:0,unit:'%'},
        {key:'load_per_core',name:'负载 / 核数',color:'#EE9B00',axis:1,unit:''},
        {key:'assigned',name:'已分配任务数',color:'#9A60B4',axis:2,unit:'个'},
        {key:'enabled',name:'已启用任务数',color:'#00A6A6',axis:2,unit:'个'}
    ];
    function historyBounds(rows, range) {
        if(!rows.length) return {};
        const first=rows[0].collected_at*1000,last=rows[rows.length-1].collected_at*1000;
        const span=last-first,selection=range || [0,100];
        let min=first+span*selection[0]/100,max=first+span*selection[1]/100;
        if(min===max && span>0) { max=Math.min(last,min+60000);min=Math.max(first,Math.min(min,max-60000)); }
        return {min,max};
    }
    function historyOption(rows, selected) {
        const visible={},samples={};
        historyMetrics.forEach(m=>{visible[m.name]=!selected || selected[m.name]!==false;});
        rows.forEach(r=>{samples[r.collected_at*1000]=r;});
        const {min,max}=historyBounds(rows);
        return {
            animation:false,
            color:historyMetrics.map(m=>m.color),
            legend:{top:4,left:'center',itemGap:16,selectedMode:'multiple',data:historyMetrics.map(m=>m.name),selected:visible},
            grid:[{left:85,right:30,top:75,height:'18%'},{left:85,right:30,top:'43%',height:'18%'},{left:85,right:30,top:'70%',bottom:45}],
            xAxis:[0,1,2].map(i=>({type:'time',gridIndex:i,min,max,axisLabel:{show:i===2},axisTick:{show:i===2},axisPointer:{show:true,snap:true,label:{show:i===2}}})),
            yAxis:[
                {type:'value',gridIndex:0,name:'资源使用率 (%)',min:0,max:100,splitNumber:2},
                {type:'value',gridIndex:1,name:'负载 / 核数',min:0,splitNumber:3},
                {type:'value',gridIndex:2,name:'任务数量 (个)',min:0,minInterval:1,splitNumber:3}
            ],
            axisPointer:{link:[{xAxisIndex:'all'}]},
            tooltip:{trigger:'axis',axisPointer:{type:'line'},formatter:params=>{
                if(!params || !params.length) return '';
                const timestamp=params[0].value[0],row=samples[timestamp] || {};
                const lines=[escapeText(new Date(timestamp).toLocaleString('zh-CN',{hour12:false}))];
                historyMetrics.forEach(m=>{
                    if(selected && selected[m.name]===false) return;
                    const value=row[m.key];
                    const text=numeric(value) ? (m.unit==='个' ? String(value) : value.toFixed(m.unit==='%' ? 1 : 2))+m.unit : '—';
                    lines.push('<span style="display:inline-block;width:9px;height:9px;border-radius:50%;margin-right:6px;background:'+m.color+'"></span>'+escapeText(m.name)+'：'+text);
                });
                return lines.join('<br>');
            }},
            series:historyMetrics.map(m=>({name:m.name,type:'line',xAxisIndex:m.axis,yAxisIndex:m.axis,showSymbol:false,connectNulls:false,itemStyle:{normal:{color:m.color}},lineStyle:{normal:{color:m.color,width:2}},data:historyPoints(rows,m.key)}))
        };
    }
    root.PPMonitor={escapeText,compare,historyPoints,historyOption,historyBounds};
    if (!root.document || !root.layui) return;
    root.layui.use(['layer','jquery','slider'],function () {
        const $=root.layui.$,layer=root.layui.layer;
        const css='<style>.pp-monitor{padding:14px;color:#333}.pp-monitor table{width:100%;border-collapse:collapse;font-size:12px}.pp-monitor th,.pp-monitor td{padding:9px;border:1px solid #e3e6ea;text-align:left}.pp-monitor th{background:#f5f7fa}.pp-monitor .note{color:#666;margin:10px 0;line-height:1.7}.pp-monitor button{margin:3px}.pp-monitor .bad{color:#b42318}.pp-monitor input[type=number]{width:90px}.pp-monitor .toolbar{margin-bottom:10px}.pp-monitor .scroller{overflow:auto}.pp-monitor select{display:inline-block;padding:5px}.pp-monitor .error{color:#b42318;white-space:pre-wrap}</style>';
        $(css).appendTo('head');
        function api(path,body) {
            return $.ajax({url:path,method:body===undefined?'GET':'POST',contentType:'application/json',headers:{'X-PPGo-Monitor':'1'},data:body===undefined?undefined:JSON.stringify(body),timeout:15000});
        }
        function failure(el,x) { el.text((x.responseJSON&&x.responseJSON.error)||'请求失败，请检查服务及数据库迁移'); }
        function time(t) { return t ? new Date(t*1000).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}) : '—'; }
        function pct(v) { return numeric(v) ? v.toFixed(1)+'%' : '—'; }
        function gib(v) { return numeric(v) && v>0 ? (v/1073741824).toFixed(1)+' GiB' : '0 GiB'; }
        function btn(label,cls) { return '<button type="button" class="layui-btn layui-btn-xs layui-btn-primary '+cls+'">'+label+'</button>'; }
        $('#monitor-open').on('click',function () {
            const fs=root.layui.formSelects,selecting=$('select[xm-select="select_server"]').length>0;
            let selected=new Set(selecting&&fs ? (fs.value('select_server','val')||[]).map(String) : []);
            let rows=[],admin=false,closed=false;
            const box=$('<div class="pp-monitor"><div class="toolbar">'+btn('刷新','refresh')+btn('告警记录','alerts')+btn('告警配置','settings')+' <label>排序 <select class="sort"><option value="assigned">已分配任务</option><option value="enabled">已启用任务</option><option value="cpu">CPU</option><option value="memory_available">可用内存</option><option value="load_per_core">负载 / 核数</option></select></label> <label><input type="checkbox" class="desc"> 降序</label></div><div class="error"></div><div class="note">每 60 秒采集。任务数是全平台配置数量，不代表并发；负载 / 核数不是 CPU 百分比。监控仅供人工选机，不会自动分配或停用任务。磁盘仅代表所示路径。</div><div class="scroller"><table><thead><tr><th>选择 / ID</th><th>服务器</th><th>分配 / 启用</th><th>CPU</th><th>内存可用 / 总量</th><th>负载 / 核数</th><th>磁盘可用</th><th>采集状态及时间</th><th>操作</th></tr></thead><tbody></tbody></table></div></div>');
            box.find('.settings').hide();
            const options={type:1,title:'主机负载对比',area:['96%','82%'],content:box,end:()=>{closed=true;clearInterval(timer);box.remove();}};
            if(selecting) { options.btn=['应用服务器选择','取消'];options.yes=i=>{if(fs) fs.value('select_server',Array.from(selected));layer.close(i);}; }
            box.appendTo('body').hide();
            layer.open(options);
            function render() {
                const key=box.find('.sort').val(),desc=box.find('.desc').prop('checked');
                box.find('tbody').html(rows.slice().sort((a,b)=>compare(a,b,key,desc)).map(r=>{
                    const fresh=r.status==='ok'||r.status==='partial';
                    return '<tr><td>'+(selecting?'<input class="pick" type="checkbox" data-id="'+r.server_id+'" '+(selected.has(String(r.server_id))?'checked':'')+'> ':'')+r.server_id+'</td><td>'+escapeText(r.name)+'</td><td>'+(r.assigned==null?'—':r.assigned)+' / '+(r.enabled==null?'—':r.enabled)+'</td><td>'+pct(r.cpu)+'<br>'+r.cpu_cores+' 核</td><td>'+ (r.memory==null?'—':gib(r.memory_available)+' / '+gib(r.memory_total)+'<br>使用 '+pct(r.memory))+'</td><td>'+(numeric(r.load_per_core)?r.load_per_core.toFixed(2):'—')+'<br>Load1: '+(numeric(r.load1)?r.load1.toFixed(2):'—')+'</td><td>'+(r.disk_free==null?'—':gib(r.disk_available)+' / '+pct(r.disk_free))+'<br>'+escapeText(r.disk_path)+'</td><td class="'+(fresh?'':'bad')+'">'+escapeText(names[r.status]||r.status)+'<br>'+time(r.collected_at)+(r.last_success&&r.last_success!==r.collected_at?'<br>最近成功 '+time(r.last_success):'')+'</td><td><button type="button" class="layui-btn layui-btn-xs trend" data-id="'+r.server_id+'">趋势</button>'+(admin?'<br><label><input type="checkbox" class="alarm" data-id="'+r.server_id+'" '+(r.alert_enabled?'checked':'')+'>单机告警</label>':'')+'</td></tr>';
                }).join('')||'<tr><td colspan="9">没有可见服务器</td></tr>');
            }
            function refresh() { api('/server/monitor').done(data=>{if(closed)return;rows=data.data;admin=data.is_admin;box.find('.settings').toggle(admin);box.find('.error').text('');render();}).fail(x=>{if(!closed)failure(box.find('.error'),x);}); }
            const timer=setInterval(refresh,60000);
            box.on('change','.pick',function(){const id=String($(this).data('id'));if(this.checked)selected.add(id);else selected.delete(id);});
            box.on('change','.sort,.desc',render).on('click','.refresh',refresh);
            box.on('click','.trend',function(){showHistory(Number($(this).data('id')));});
            box.on('click','.settings',showSettings).on('click','.alerts',showAlerts);
            box.on('change','.alarm',function(){const input=this;input.disabled=true;api('/server/monitoralert',{server_id:Number($(this).data('id')),enabled:this.checked}).done(refresh).fail(x=>{input.checked=!input.checked;failure(box.find('.error'),x);}).always(()=>{input.disabled=false;});});
            refresh();
        });
        function showHistory(id) {
            const box=$('<div class="pp-monitor"><select class="range"><option value="1h">近 1 小时</option><option value="24h">近 24 小时</option><option value="72h">近 3 天</option></select><div class="error"></div><div class="note">点击图例可隐藏或恢复曲线；拖动底部滑块可同步缩放三组图表。分钟采样，缺失指标与采集间断不会补零或连线。时间为浏览器本地时区。</div><div class="chart"></div><div style="margin:4px 30px 10px 85px"><div class="history-zoom"></div><div class="note" style="display:flex;justify-content:space-between;margin-top:12px"><span class="zoom-from"></span><span class="zoom-to"></span></div></div></div>');
            let rows=[],disposed=false,request=0,chart;
            const selected={};
            function resize(){if(disposed)return;box.find('.chart').height(Math.max(480,Math.floor(root.innerHeight*0.9)-210));if(chart)chart.resize();}
            resize();
            box.appendTo('body').hide();
            layer.open({type:1,title:'服务器 #'+id+' 历史趋势',area:['90%','90%'],content:box,success:()=>{chart=root.echarts.init(box.find('.chart')[0]);chart.on('legendselectchanged',event=>Object.assign(selected,event.selected));root.addEventListener('resize',resize);},end:()=>{disposed=true;root.removeEventListener('resize',resize);if(chart)chart.dispose();box.remove();}});
            function zoom(range){
                if(disposed||!chart)return;
                const bounds=historyBounds(rows,range);
                chart.setOption({xAxis:[0,1,2].map(()=>({min:bounds.min,max:bounds.max}))});
                box.find('.zoom-from').text(bounds.min===undefined ? '' : new Date(bounds.min).toLocaleString('zh-CN',{hour12:false}));
                box.find('.zoom-to').text(bounds.max===undefined ? '' : new Date(bounds.max).toLocaleString('zh-CN',{hour12:false}));
            }
            function draw(){
                if(disposed||!chart)return;
                chart.setOption(historyOption(rows,selected),true);
                // The bundled ECharts build omits dataZoom; reuse Layui's range slider.
                root.layui.slider.render({elem:box.find('.history-zoom')[0],range:true,value:[0,100],theme:'#5470C6',disabled:rows.length<2,setTips:value=>value+'%',change:zoom});
                zoom([0,100]);
            }
            function load(){const n=++request;api('/server/monitorhistory?server_id='+id+'&range='+box.find('.range').val()).done(data=>{if(disposed||n!==request)return;rows=data.data;box.find('.error').text(rows.length?'':'暂无历史数据');draw();}).fail(x=>{if(!disposed)failure(box.find('.error'),x);});}
            box.on('change','.range',load);load();
        }
        function showSettings() {
            api('/server/monitorsettings').done(data=>{
                const c=data.settings;
                const box=$('<div class="pp-monitor"><div class="error"></div><label><input type="checkbox" class="enabled"> 开启全局邮件告警</label><div class="note">关闭会结束活动告警并取消待发消息，不发送恢复邮件。只发送首次与恢复；投递失败会有限重试。复用已有 SMTP 配置，支持 25 / 465 端口。启用前请在部署环境确认收件人与邮箱可用性。</div><table><thead><tr><th>规则</th><th>阈值 (%)</th><th>持续时间 (秒)</th></tr></thead><tbody></tbody></table><p>采集异常：连续 <input type="number" class="failures" min="2" max="60"> 次失败</p><p>收件人（可多选，仅使用启用用户的邮箱）</p><div class="people"></div><p>恢复条件：连续两次有效采样正常。全局规则修改会重新计算尚未触发的持续时间。</p></div>');
                box.find('.enabled').prop('checked',c.enabled);box.find('.failures').val(c.failure_count);
                box.find('tbody').html(['cpu','memory','disk'].map(k=>'<tr data-rule="'+k+'"><td>'+({cpu:'CPU ≥',memory:'内存 ≥',disk:'磁盘可用 <'}[k])+'</td><td><input type="number" class="threshold" min="0.1" max="99.9" step="0.1" value="'+c[k].threshold+'"></td><td><input type="number" class="seconds" min="60" max="86400" value="'+c[k].seconds+'"></td></tr>').join(''));
                box.find('.people').html(data.people.map(p=>'<label style="display:block"><input type="checkbox" value="'+p.id+'" '+(c.recipient_ids.includes(p.id)?'checked':'')+'> '+escapeText(p.name)+' — '+escapeText(p.email||'未填写邮箱')+'</label>').join(''));
                box.appendTo('body').hide();
                layer.open({type:1,title:'全局资源告警配置',area:['760px','660px'],content:box,end:()=>box.remove(),btn:['保存','取消'],yes:i=>{
                    const next={enabled:box.find('.enabled').prop('checked'),failure_count:Number(box.find('.failures').val()),recipient_ids:box.find('.people input:checked').map(function(){return Number(this.value);}).get()};
                    box.find('tr[data-rule]').each(function(){next[$(this).data('rule')]={threshold:Number($(this).find('.threshold').val()),seconds:Number($(this).find('.seconds').val())};});
                    if(next.enabled&&!root.confirm('确认开启资源告警，并向所选邮箱发送真实首次告警及恢复通知？'))return;
                    api('/server/monitorsettings',next).done(()=>{layer.close(i);layer.msg('配置已保存');}).fail(x=>failure(box.find('.error'),x));
                }});
            }).fail(x=>layer.msg((x.responseJSON&&x.responseJSON.error)||'配置读取失败'));
        }
        function showAlerts() {
            api('/server/monitoralerts').done(data=>{
                const state={pending:'等待投递',sending:'正在提交',sent:'已提交 SMTP',failed:'投递失败',cancelled:'已取消'};
                const metric={cpu:'CPU',memory:'内存',disk:'磁盘',availability:'采集异常'};
                const box=$('<div class="pp-monitor"><h3>当前告警</h3><div class="active"></div><p class="note">以下展示最近 200 条通知记录，每个收件人独立记录。已提交 SMTP 不代表已读。</p><div class="scroller"><table><thead><tr><th>时间</th><th>服务器</th><th>类型</th><th>邮件状态</th><th>尝试次数</th><th>说明</th></tr></thead><tbody></tbody></table></div></div>');
                box.find('.active').html(data.active.map(a=>'<p class="bad">服务器 #'+a.server_id+' · '+escapeText(metric[a.metric])+' · 开始于 '+time(a.started_at)+'</p>').join('')||'<p>无活动告警</p>');
                box.find('tbody').html(data.events.map(e=>'<tr><td>'+time(e.created_at)+'</td><td>#'+e.server_id+'</td><td>'+escapeText(metric[e.metric])+' / '+(e.kind==='firing'?'首次':'恢复')+'</td><td>'+escapeText(state[e.status])+'</td><td>'+e.attempts+'</td><td>'+escapeText(e.error||e.subject)+'</td></tr>').join(''));
                box.appendTo('body').hide();
                layer.open({type:1,title:'资源告警与邮件记录',area:['90%','80%'],content:box,end:()=>box.remove()});
            }).fail(x=>layer.msg((x.responseJSON&&x.responseJSON.error)||'告警读取失败'));
        }
    });
})(typeof window !== 'undefined' ? window : globalThis);
