package monitor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/models"
	"github.com/george518/PPGo_Job/notify"
)

// The existing deployment is a single controller. Serialize state transitions
// and outbox claims, but never hold this lock during network delivery.
var stateMu sync.Mutex

func transaction(fn func(orm.Ormer) error) error {
	o := orm.NewOrm()
	if err := o.Begin(); err != nil {
		return err
	}
	if err := fn(o); err != nil {
		_ = o.Rollback()
		return err
	}
	return o.Commit()
}

func readSettings(o orm.Ormer) (Settings, error) {
	c := DefaultSettings()
	row := models.MonitorConfig{Id: 1}
	err := o.Read(&row)
	if err == orm.ErrNoRows {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(row.Data), &c)
	if err == nil {
		err = c.Validate()
	}
	return c, err
}
func ReadSettings() (Settings, error) { return readSettings(orm.NewOrm()) }

func recipients(o orm.Ormer, ids []int) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		u := models.Admin{Id: id}
		if id <= 0 {
			return nil, fmt.Errorf("无效收件人")
		}
		if err := o.Read(&u); err != nil {
			return nil, fmt.Errorf("收件人不存在")
		}
		a, err := mail.ParseAddress(strings.TrimSpace(u.Email))
		if u.Status != 1 || err != nil || strings.ContainsAny(u.Email, "\r\n") {
			return nil, fmt.Errorf("收件人必须启用并填写有效邮箱")
		}
		key := strings.ToLower(a.Address)
		if !seen[key] {
			result = append(result, a.Address)
			seen[key] = true
		}
	}
	return result, nil
}

func initialize() error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		if _, err := o.QueryTable(new(models.MonitorSample)).Count(); err != nil {
			return err
		}
		if _, err := o.QueryTable(new(models.MonitorState)).Count(); err != nil {
			return err
		}
		if _, err := readSettings(o); err != nil {
			return err
		}
		// A process can stop after SMTP acceptance but before this update. SMTP
		// cannot guarantee exactly-once delivery; durable events avoid routine duplicates.
		if _, err := o.QueryTable(new(models.MonitorEvent)).Filter("status", "sending").Filter("attempts__gte", 4).Update(orm.Params{"status": "failed", "last_error": "发送中断，已达到尝试次数上限"}); err != nil {
			return err
		}
		_, err := o.QueryTable(new(models.MonitorEvent)).Filter("status", "sending").Update(orm.Params{"status": "pending", "next_attempt": time.Now().Unix()})
		return err
	})
}

func SaveSettings(c Settings) error {
	if err := c.Validate(); err != nil {
		return err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		if _, err := recipients(o, c.RecipientIDs); err != nil {
			return err
		}
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		row := models.MonitorConfig{Id: 1}
		err = o.Read(&row)
		if err != nil && err != orm.ErrNoRows {
			return err
		}
		row.Data = string(data)
		if err == orm.ErrNoRows {
			_, err = o.Insert(&row)
		} else {
			_, err = o.Update(&row)
		}
		if err != nil {
			return err
		}
		var states []*models.MonitorState
		if _, err = o.QueryTable(new(models.MonitorState)).All(&states); err != nil {
			return err
		}
		for _, row := range states {
			var s AlertState
			if err = json.Unmarshal([]byte(row.Data), &s); err != nil {
				return err
			}
			s.PendingSince = 0
			s.Failures = 0
			s.Recoveries = 0
			s.LastEval = 0
			if !c.Enabled {
				if err = closeIncident(o, s.Incident, true, time.Now().Unix()); err != nil {
					return err
				}
				s.Active = false
			}
			if err = saveState(o, row, s); err != nil {
				return err
			}
		}
		return nil
	})
}

func SetServerAlert(id int, enabled bool) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		srv := models.TaskServer{Id: id}
		if err := o.Read(&srv); err != nil {
			return err
		}
		srv.MonitorAlertDisabled = !enabled
		if _, err := o.Update(&srv, "MonitorAlertDisabled"); err != nil {
			return err
		}
		var rows []*models.MonitorState
		if _, err := o.QueryTable(new(models.MonitorState)).Filter("server_id", id).All(&rows); err != nil {
			return err
		}
		for _, row := range rows {
			var s AlertState
			if err := json.Unmarshal([]byte(row.Data), &s); err != nil {
				return err
			}
			if err := closeIncident(o, s.Incident, true, time.Now().Unix()); err != nil {
				return err
			}
			s.Active = false
			s.PendingSince = 0
			s.Failures = 0
			s.Recoveries = 0
			s.LastEval = 0
			if err := saveState(o, row, s); err != nil {
				return err
			}
		}
		return nil
	})
}

func saveState(o orm.Ormer, row *models.MonitorState, s AlertState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	row.Data = string(b)
	if row.Id == 0 {
		_, err = o.Insert(row)
	} else {
		_, err = o.Update(row, "Data")
	}
	return err
}

// Deleted resources and resources switched away from Agent must not retain
// active incidents or deliver stale notifications.
func retireServers(eligible map[int]bool, now int64) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		var rows []*models.MonitorState
		if _, err := o.QueryTable(new(models.MonitorState)).Limit(-1).All(&rows); err != nil {
			return err
		}
		for _, row := range rows {
			if eligible[row.ServerId] {
				continue
			}
			var s AlertState
			if err := json.Unmarshal([]byte(row.Data), &s); err != nil {
				return err
			}
			if err := closeIncident(o, s.Incident, true, now); err != nil {
				return err
			}
			if _, err := o.Delete(row); err != nil {
				return err
			}
		}
		return nil
	})
}

var metricNames = map[string]string{"cpu": "CPU 使用率", "memory": "内存使用率", "disk": "磁盘可用比例", "availability": "监控采集异常"}

func processSample(server *models.TaskServer, sample Snapshot) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		// A per-server switch may change while its RPC is in flight.
		current := models.TaskServer{Id: server.Id}
		if err := o.Read(&current); err != nil {
			return err
		}
		server = &current
		cfg, err := readSettings(o)
		if err != nil {
			return err
		}
		for _, metric := range []string{"cpu", "memory", "disk", "availability"} {
			row := models.MonitorState{ServerId: server.Id, Metric: metric}
			err = o.Read(&row, "ServerId", "Metric")
			if err != nil && err != orm.ErrNoRows {
				return err
			}
			var state AlertState
			if row.Id != 0 {
				if err = json.Unmarshal([]byte(row.Data), &state); err != nil {
					return err
				}
			}
			reachable := sample.Status == "ok" || sample.Status == "partial"
			if metric == "availability" && reachable {
				state.Seen = true
			}
			if !cfg.Enabled || server.MonitorAlertDisabled || server.Status == 2 || server.ConnectionType != 2 {
				if state.Active {
					if err = closeIncident(o, state.Incident, true, sample.CollectedAt); err != nil {
						return err
					}
				}
				state.Active = false
				state.PendingSince = 0
				state.Failures = 0
				state.Recoveries = 0
				state.LastEval = sample.CollectedAt
				if err = saveState(o, &row, state); err != nil {
					return err
				}
				continue
			}
			var value *float64
			switch metric {
			case "cpu":
				value = sample.CPU
			case "memory":
				value = sample.Memory
			case "disk":
				value = sample.DiskFree
			}
			next, transition := Evaluate(state, metric, value, reachable, cfg, sample.CollectedAt)
			if transition == "firing" {
				b := make([]byte, 16)
				if _, err = rand.Read(b); err != nil {
					return err
				}
				next.Incident = hex.EncodeToString(b)
				to, err := recipients(o, cfg.RecipientIDs)
				if err != nil {
					return err
				}
				current := "连续采集失败（不等同于主机宕机）"
				threshold := fmt.Sprintf("连续 %d 次", cfg.FailureCount)
				if value != nil {
					current = fmt.Sprintf("%.2f%%", *value)
					r := cfg.CPU
					if metric == "memory" {
						r = cfg.Memory
					}
					if metric == "disk" {
						r = cfg.Disk
					}
					threshold = fmt.Sprintf("%.2f%%，持续 %d 秒", r.Threshold, r.Seconds)
				}
				body := fmt.Sprintf("服务器：%s（ID %d）\n指标：%s\n当前：%s\n阈值：%s\n异常开始：%s\n采集时间：%s\n磁盘路径：%s\n查看：登录 PPGo → 执行资源 → 主机监控 → 趋势\n", server.ServerName, server.Id, metricNames[metric], current, threshold, stamp(next.StartedAt), stamp(sample.CollectedAt), sample.DiskPath)
				for _, address := range to {
					e := models.MonitorEvent{ServerId: server.Id, Incident: next.Incident, Kind: "firing", Metric: metric, Recipient: address, CreatedAt: sample.CollectedAt, Status: "pending", NextAttempt: sample.CollectedAt, Subject: "[PPGo 资源告警] " + server.ServerName + " / " + metricNames[metric], Body: body}
					// Resource names may already occupy all 255 characters allowed
					// by the original schema. Keep the full name in the message body.
					if title := []rune(e.Subject); len(title) > 255 {
						e.Subject = string(title[:255])
					}
					e.RecipientKey = recipientKey(address)
					if _, err = o.Insert(&e); err != nil {
						return err
					}
				}
			} else if transition == "resolved" {
				if err = closeIncident(o, next.Incident, false, sample.CollectedAt); err != nil {
					return err
				}
			}
			if err = saveState(o, &row, next); err != nil {
				return err
			}
		}
		return nil
	})
}

func stamp(t int64) string {
	return time.Unix(t, 0).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05")
}

func recipientKey(address string) string {
	h := sha256.Sum256([]byte(strings.ToLower(address)))
	return hex.EncodeToString(h[:])
}

func recovery(o orm.Ormer, first *models.MonitorEvent) error {
	count, err := o.QueryTable(new(models.MonitorEvent)).Filter("incident", first.Incident).Filter("kind", "resolved").Filter("recipient", first.Recipient).Count()
	if err != nil || count > 0 {
		return err
	}
	e := models.MonitorEvent{ServerId: first.ServerId, Incident: first.Incident, Kind: "resolved", Metric: first.Metric, Recipient: first.Recipient, CreatedAt: first.ClosedAt, ClosedAt: first.ClosedAt, Status: "pending", NextAttempt: time.Now().Unix(), Subject: strings.Replace(first.Subject, "资源告警", "资源恢复", 1), Body: "已连续两次有效采样恢复正常。\n恢复时间：" + stamp(first.ClosedAt) + "\n\n原告警：\n" + first.Body}
	e.RecipientKey = first.RecipientKey
	_, err = o.Insert(&e)
	return err
}

func closeIncident(o orm.Ormer, incident string, suppress bool, now int64) error {
	if incident == "" {
		return nil
	}
	var events []*models.MonitorEvent
	if _, err := o.QueryTable(new(models.MonitorEvent)).Filter("incident", incident).All(&events); err != nil {
		return err
	}
	for _, e := range events {
		e.ClosedAt = now
		e.SuppressRecovery = suppress
		if e.Status == "pending" {
			e.Status = "cancelled"
		}
		if _, err := o.Update(e, "ClosedAt", "SuppressRecovery", "Status"); err != nil {
			return err
		}
		if e.Kind == "firing" && e.Status == "sent" && !suppress {
			if err := recovery(o, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func deliverLoop() {
	for {
		if err := deliverOne(time.Now().Unix(), notify.SendMonitorMail); err != nil {
			beego.Error("monitor mail delivery: ", err)
		}
		time.Sleep(time.Second)
	}
}

func deliverOne(now int64, send func(string, string, string) error) error {
	var event models.MonitorEvent
	stateMu.Lock()
	err := transaction(func(o orm.Ormer) error {
		var rows []*models.MonitorEvent
		_, err := o.QueryTable(new(models.MonitorEvent)).Filter("status", "pending").Filter("next_attempt__lte", now).OrderBy("id").Limit(1).All(&rows)
		if err != nil || len(rows) == 0 {
			return err
		}
		event = *rows[0]
		cfg, err := readSettings(o)
		if err != nil {
			return err
		}
		server := models.TaskServer{Id: event.ServerId}
		if err = o.Read(&server); err != nil && err != orm.ErrNoRows {
			return err
		}
		if !cfg.Enabled || err == orm.ErrNoRows || server.Status == 2 || server.ConnectionType != 2 || server.MonitorAlertDisabled || (event.Kind == "firing" && event.ClosedAt > 0) {
			event.Status = "cancelled"
			event.ClosedAt = now
			event.SuppressRecovery = true
		} else {
			event.Status = "sending"
			event.Attempts++
		}
		_, err = o.Update(&event)
		return err
	})
	stateMu.Unlock()
	if err != nil || event.Id == 0 || event.Status != "sending" {
		return err
	}
	sendErr := send(event.Recipient, event.Subject, event.Body)
	stateMu.Lock()
	defer stateMu.Unlock()
	return transaction(func(o orm.Ormer) error {
		if err := o.Read(&event); err != nil {
			return err
		}
		if sendErr == nil {
			event.Status = "sent"
			event.LastError = ""
			if event.Kind == "firing" && event.ClosedAt > 0 && !event.SuppressRecovery {
				if err := recovery(o, &event); err != nil {
					return err
				}
			}
		} else {
			// Keep transport details (which may contain addresses) out of browser responses.
			event.LastError = "SMTP 投递失败，请检查 SMTP 配置、网络及收件人"
			event.Status = "failed"
			if event.SuppressRecovery || (event.Kind == "firing" && event.ClosedAt > 0) {
				event.Status = "cancelled"
			} else if event.Attempts < 4 {
				event.Status = "pending"
				event.NextAttempt = time.Now().Unix() + []int64{60, 300, 900}[event.Attempts-1]
			}
		}
		_, err := o.Update(&event)
		return err
	})
}
