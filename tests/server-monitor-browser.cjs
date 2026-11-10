// Local-only UI fixture. All monitoring responses and writes stay in memory;
// no scheduler, Agent, database or mail service is started.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const now = Math.floor(Date.now()/1000);
const rows = [1,2,3].map(id=>({server_id:id,name:'Fixture '+id,group_id:1,connection_type:2,alert_enabled:true,collected_at:now,last_success:now,status:id===2?'failed':'ok',cpu_cores:4,cpu:id===2?null:id===1?70:20,memory:40,memory_available:8*1073741824,memory_total:16*1073741824,load1:2,load_per_core:0.5,disk_free:60,disk_available:60*1073741824,disk_path:'/data',assigned:id*3,enabled:id,errors:[]}));
let settings={enabled:false,cpu:{threshold:90,seconds:300},memory:{threshold:90,seconds:180},disk:{threshold:10,seconds:180},failure_count:3,recipient_ids:[1]};
const html=`<!doctype html><meta charset="utf-8"><title>PPGo monitor isolated UI fixture</title><link rel="stylesheet" href="/static/layui/css/layui.css"><script src="/static/layui/layui.js"></script>
<main style="padding:24px"><h2>Isolated monitoring fixture — no real mail</h2><form class="layui-form"><input id="task-name" value="keep my task"><select xm-select="select_server"><option value="0">本机</option><option value="1">Fixture 1</option><option value="2">Fixture 2</option><option value="3">Fixture 3</option></select></form><button type="button" id="monitor-open">查看主机负载</button></main>
<script src="/static/echarts/echarts.min.js"></script><script src="/static/admin/js/server-monitor.js"></script><script>layui.config({base:'/static/admin/js/'}).extend({formSelects:'formSelects-v3'}).use(['form','formSelects'],function(){layui.formSelects.render({name:'select_server',init:[0,1]});});</script>`;
http.createServer(async (req,res)=>{
    const url=new URL(req.url,'http://127.0.0.1');
    function json(v){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));}
    if(url.pathname==='/'){res.setHeader('Content-Type','text/html; charset=utf-8');return res.end(html);}
    if(url.pathname==='/server/monitor')return json({data:rows,is_admin:true});
    if(url.pathname==='/server/monitorhistory')return json({data:Array.from({length:60},(_,i)=>({...rows[0],collected_at:now-3600+i*60,cpu:i===20?null:20+(i%10)*5}))});
    if(url.pathname==='/server/monitoralerts')return json({active:[],events:[]});
    if(url.pathname==='/server/monitorsettings'&&req.method==='GET')return json({settings,people:[{id:1,name:'Test operator',email:'test@example.invalid'}]});
    if(req.method==='POST'&&['/server/monitorsettings','/server/monitoralert'].includes(url.pathname)){
        let body='';for await(const chunk of req)body+=chunk;
        const value=JSON.parse(body);
        if(url.pathname.endsWith('monitorsettings'))settings=value;
        else rows.find(r=>r.server_id===value.server_id).alert_enabled=value.enabled;
        return json({ok:true});
    }
    const file=path.resolve(root,'.'+decodeURIComponent(url.pathname));
    if(!url.pathname.startsWith('/static/')||!file.startsWith(root+path.sep)){res.statusCode=404;return res.end();}
    fs.readFile(file,(err,data)=>{if(err){res.statusCode=404;return res.end();}res.setHeader('Content-Type',file.endsWith('.js')?'application/javascript':file.endsWith('.css')?'text/css':'application/octet-stream');res.end(data);});
}).listen(18081,'127.0.0.1',()=>console.log('UI fixture: http://127.0.0.1:18081'));
