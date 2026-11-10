package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/libs"
	"github.com/george518/PPGo_Job/models"
	"github.com/george518/PPGo_Job/monitor"
)

// Monitoring has explicit authentication and data authorization; it does not
// inherit the legacy action-name substring allowlist.
type MonitorController struct{ BaseController }

func (c *MonitorController) Prepare() {
	parts := strings.Split(c.Ctx.GetCookie("auth"), "|")
	if len(parts) != 2 {
		c.monitorError(401, "请先登录")
		return
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		c.monitorError(401, "请先登录")
		return
	}
	u, err := models.AdminGetById(id)
	if err != nil || u.Status != 1 || parts[1] != libs.Md5([]byte(c.getClientIp()+"|"+u.Password+u.Salt)) {
		c.monitorError(401, "登录已失效")
		return
	}
	c.userId = id
	c.user = u
	c.dataAuth(u)
	if c.Ctx.Request.Method != "GET" {
		if c.userId != 1 {
			c.monitorError(403, "仅超级管理员可修改监控告警")
			return
		}
		if c.Ctx.Request.Header.Get("X-PPGo-Monitor") != "1" {
			c.monitorError(403, "缺少请求校验头")
			return
		}
		if origin := c.Ctx.Request.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != c.Ctx.Request.Host {
				c.monitorError(403, "请求来源无效")
				return
			}
		}
	}
}
func (c *MonitorController) monitorJSON(data interface{}) { c.Data["json"] = data; c.ServeJSON() }
func (c *MonitorController) monitorError(code int, msg string) {
	c.Ctx.Output.SetStatus(code)
	c.monitorJSON(map[string]interface{}{"error": msg})
	c.StopRun()
}
func (c *MonitorController) servers() []*models.TaskServer {
	q := orm.NewOrm().QueryTable(new(models.TaskServer)).Filter("status__in", []int{0, 1})
	if c.userId != 1 {
		ids := taskGroupIDs(c.serverGroups)
		if len(ids) == 0 {
			return []*models.TaskServer{}
		}
		q = q.Filter("group_id__in", ids)
	}
	var rows []*models.TaskServer
	if _, err := q.OrderBy("id").All(&rows); err != nil {
		c.monitorError(503, "服务器信息暂不可用")
	}
	return rows
}
func (c *MonitorController) checkServer(id int) {
	for _, s := range c.servers() {
		if s.Id == id {
			return
		}
	}
	c.monitorError(403, "无权访问该服务器")
}
func (c *MonitorController) decode(v interface{}) {
	c.Ctx.Request.Body = http.MaxBytesReader(c.Ctx.ResponseWriter, c.Ctx.Request.Body, 16384)
	d := json.NewDecoder(c.Ctx.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		c.monitorError(400, "请求内容无效")
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		c.monitorError(400, "请求内容无效")
	}
}
func (c *MonitorController) Latest() {
	c.monitorJSON(map[string]interface{}{"data": monitor.Latest(c.servers()), "is_admin": c.userId == 1})
}
func (c *MonitorController) History() {
	id, err := c.GetInt("server_id")
	if err != nil || id <= 0 {
		c.monitorError(400, "服务器参数无效")
	}
	c.checkServer(id)
	seconds, ok := map[string]int64{"1h": 3600, "24h": 86400, "72h": monitor.Retention}[c.GetString("range")]
	if !ok {
		c.monitorError(400, "时间范围无效")
	}
	rows, err := monitor.History(id, seconds)
	if err != nil {
		c.monitorError(503, "历史数据暂不可用，请检查监控迁移")
	}
	c.monitorJSON(map[string]interface{}{"data": rows})
}
func (c *MonitorController) Settings() {
	if c.userId != 1 {
		c.monitorError(403, "仅超级管理员可查看告警配置")
	}
	cfg, err := monitor.ReadSettings()
	if err != nil {
		c.monitorError(503, "监控配置暂不可用，请检查迁移")
	}
	var users []*models.Admin
	if _, err = orm.NewOrm().QueryTable(new(models.Admin)).Filter("status", 1).All(&users, "Id", "RealName", "LoginName", "Email"); err != nil {
		c.monitorError(503, "收件人查询失败")
	}
	people := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		people = append(people, map[string]interface{}{"id": u.Id, "name": u.RealName + " (" + u.LoginName + ")", "email": u.Email})
	}
	c.monitorJSON(map[string]interface{}{"settings": cfg, "people": people})
}
func (c *MonitorController) SaveSettings() {
	var cfg monitor.Settings
	c.decode(&cfg)
	if err := monitor.SaveSettings(cfg); err != nil {
		c.monitorError(400, err.Error())
	}
	c.monitorJSON(map[string]bool{"ok": true})
}
func (c *MonitorController) ServerAlert() {
	var req struct {
		ServerID int  `json:"server_id"`
		Enabled  bool `json:"enabled"`
	}
	c.decode(&req)
	c.checkServer(req.ServerID)
	if err := monitor.SetServerAlert(req.ServerID, req.Enabled); err != nil {
		c.monitorError(503, "告警开关保存失败")
	}
	c.monitorJSON(map[string]bool{"ok": true})
}
func (c *MonitorController) Alerts() {
	servers := c.servers()
	ids := make([]int, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.Id)
	}
	events := make([]map[string]interface{}, 0)
	active := make([]map[string]interface{}, 0)
	if len(ids) > 0 {
		var rows []*models.MonitorEvent
		if _, err := orm.NewOrm().QueryTable(new(models.MonitorEvent)).Filter("server_id__in", ids).OrderBy("-id").Limit(200).All(&rows); err != nil {
			c.monitorError(503, "告警记录暂不可用")
		}
		for _, e := range rows {
			events = append(events, map[string]interface{}{"server_id": e.ServerId, "metric": e.Metric, "kind": e.Kind, "created_at": e.CreatedAt, "closed_at": e.ClosedAt, "status": e.Status, "attempts": e.Attempts, "subject": e.Subject, "error": e.LastError})
		}
		var states []*models.MonitorState
		if _, err := orm.NewOrm().QueryTable(new(models.MonitorState)).Filter("server_id__in", ids).All(&states); err != nil {
			c.monitorError(503, "告警状态暂不可用")
		}
		for _, row := range states {
			var s monitor.AlertState
			if json.Unmarshal([]byte(row.Data), &s) == nil && s.Active {
				active = append(active, map[string]interface{}{"server_id": row.ServerId, "metric": row.Metric, "started_at": s.StartedAt})
			}
		}
	}
	c.monitorJSON(map[string]interface{}{"active": active, "events": events})
}
