package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/common"
	"github.com/george518/PPGo_Job/models"
)

type previous struct {
	Metrics common.HostMetrics
	At      int64
	Address string
}

var cache = struct {
	sync.RWMutex
	latest   map[int]Snapshot
	previous map[int]previous
}{latest: make(map[int]Snapshot), previous: make(map[int]previous)}
var startOnce sync.Once

func Start() {
	startOnce.Do(func() {
		go func() {
			ready := false
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if !ready {
					if err := initialize(); err != nil {
						beego.Error("monitor initialization (check migration): ", err)
					} else {
						ready = true
						go deliverLoop()
					}
				}
				if ready {
					if err := collectCycle(time.Now().Unix()); err != nil {
						beego.Error("monitor collection: ", err)
					}
				}
				<-ticker.C
			}
		}()
	})
}

func poll(address string) (common.HostMetrics, error) {
	var m common.HostMetrics
	deadline := time.Now().Add(5 * time.Second)
	conn, err := net.DialTimeout("tcp", address, time.Until(deadline))
	if err != nil {
		return m, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(deadline); err != nil {
		return m, err
	}
	client := rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	defer client.Close()
	err = client.Call("RpcTask.GetMetrics", struct{}{}, &m)
	if err == nil && m.Version != 1 {
		err = fmt.Errorf("unsupported metric version")
	}
	return m, err
}

func number(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
func hasError(m common.HostMetrics, name string) bool {
	for _, e := range m.Errors {
		if e == name {
			return true
		}
	}
	return false
}

func metricSnapshot(id int, m common.HostMetrics, prev previous, address string, now int64) Snapshot {
	s := Snapshot{ServerID: id, CollectedAt: now, LastSuccess: now, Status: "ok", Errors: m.Errors, CPUCores: m.CPUCores, DiskPath: m.DiskPath}
	if len(m.Errors) > 0 {
		s.Status = "partial"
	}
	if m.CPUCores < 1 || m.CPUCores > 65536 {
		s.CPUCores = 0
	}
	p := prev.Metrics
	if !hasError(m, "cpu") && m.BootID != "" && m.BootID == p.BootID && address == prev.Address && now > prev.At && now-prev.At <= 90 && m.CPUTotal > p.CPUTotal && m.CPUIdle >= p.CPUIdle && m.CPUIOWait >= p.CPUIOWait {
		total := m.CPUTotal - p.CPUTotal
		idle := m.CPUIdle - p.CPUIdle
		wait := m.CPUIOWait - p.CPUIOWait
		if idle <= total && wait <= total-idle {
			s.CPU = number(100 * float64(total-idle-wait) / float64(total))
		}
	}
	if !hasError(m, "memory") && m.MemoryTotal > 0 && m.MemoryAvailable <= m.MemoryTotal {
		s.MemoryTotal = m.MemoryTotal
		s.MemoryAvailable = m.MemoryAvailable
		s.Memory = number(100 * (1 - float64(m.MemoryAvailable)/float64(m.MemoryTotal)))
	}
	if !hasError(m, "load") && m.Load1 >= 0 && m.Load5 >= 0 && m.Load15 >= 0 {
		s.Load1 = number(m.Load1)
		s.Load5 = number(m.Load5)
		s.Load15 = number(m.Load15)
		if s.CPUCores > 0 {
			s.LoadPerCore = number(m.Load1 / float64(s.CPUCores))
		}
	}
	if !hasError(m, "disk") && m.DiskTotal > 0 && m.DiskAvailable <= m.DiskTotal {
		s.DiskTotal = m.DiskTotal
		s.DiskAvailable = m.DiskAvailable
		s.DiskFree = number(100 * float64(m.DiskAvailable) / float64(m.DiskTotal))
	}
	return s
}

// CountAssignments counts exact, unique CSV IDs; rotation still binds a task to
// each configured node. These are configuration counts, never concurrency.
func CountAssignments(tasks []*models.Task) map[int][2]int {
	out := make(map[int][2]int)
	for _, t := range tasks {
		if t.Status < 0 || t.Status > 3 {
			continue
		}
		seen := make(map[int]bool)
		for _, raw := range strings.Split(t.ServerIds, ",") {
			id, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || id < 0 || seen[id] {
				continue
			}
			seen[id] = true
			v := out[id]
			v[0]++
			if t.Status == 1 {
				v[1]++
			}
			out[id] = v
		}
	}
	return out
}

var lastCleanup int64

func collectCycle(now int64) error {
	o := orm.NewOrm()
	var servers []*models.TaskServer
	if _, err := o.QueryTable(new(models.TaskServer)).Filter("status__in", []int{0, 1}).All(&servers); err != nil {
		return err
	}
	var tasks []*models.Task
	_, countErr := o.QueryTable(new(models.Task)).Filter("status__in", []int{0, 1, 2, 3}).Limit(-1).All(&tasks, "Id", "ServerIds", "Status")
	counts := CountAssignments(tasks)
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		sem <- struct{}{}
		go func(server *models.TaskServer) {
			defer wg.Done()
			defer func() { <-sem }()
			address := net.JoinHostPort(server.ServerIp, strconv.Itoa(server.Port))
			cache.RLock()
			p := cache.previous[server.Id]
			old := cache.latest[server.Id]
			cache.RUnlock()
			s := Snapshot{ServerID: server.Id, CollectedAt: now, Status: "unmonitored", LastSuccess: old.LastSuccess}
			if server.ConnectionType == 2 {
				m, err := poll(address)
				if err != nil {
					s.Status = "failed"
					if strings.Contains(err.Error(), "can't find") || strings.Contains(err.Error(), "unsupported") {
						s.Status = "unsupported"
					}
					cache.Lock()
					delete(cache.previous, server.Id)
					cache.Unlock()
				} else {
					s = metricSnapshot(server.Id, m, p, address, now)
					cache.Lock()
					cache.previous[server.Id] = previous{m, now, address}
					cache.Unlock()
				}
			}
			if countErr == nil {
				c := counts[server.Id]
				s.Assigned = &c[0]
				s.Enabled = &c[1]
			}
			s.Name = server.ServerName
			s.GroupID = server.GroupId
			s.ConnectionType = server.ConnectionType
			s.AlertEnabled = !server.MonitorAlertDisabled
			cache.Lock()
			cache.latest[server.Id] = s
			cache.Unlock()
			if server.ConnectionType != 2 {
				return
			}
			data, err := json.Marshal(s)
			if err != nil {
				beego.Error("monitor encode: ", err)
				return
			}
			if _, err = orm.NewOrm().Insert(&models.MonitorSample{ServerId: server.Id, CollectedAt: now, Data: string(data)}); err != nil {
				beego.Error("monitor sample save: ", err)
			}
			if err = processSample(server, s); err != nil {
				beego.Error("monitor alert state: ", err)
			}
		}(server)
	}
	wg.Wait()
	eligible := make(map[int]bool)
	for _, server := range servers {
		if server.ConnectionType == 2 {
			eligible[server.Id] = true
		}
	}
	if err := retireServers(eligible, now); err != nil {
		return err
	}
	valid := make(map[int]bool)
	for _, s := range servers {
		valid[s.Id] = true
	}
	cache.Lock()
	for id := range cache.latest {
		if !valid[id] {
			delete(cache.latest, id)
			delete(cache.previous, id)
		}
	}
	cache.Unlock()
	if now-lastCleanup >= 3600 {
		if err := cleanup(now); err != nil {
			return err
		}
		lastCleanup = now
	}
	return countErr
}

func Latest(servers []*models.TaskServer) []Snapshot {
	cache.RLock()
	defer cache.RUnlock()
	rows := make([]Snapshot, 0, len(servers))
	now := time.Now().Unix()
	for _, server := range servers {
		s, ok := cache.latest[server.Id]
		if !ok {
			s = Snapshot{Status: "waiting"}
			if server.ConnectionType != 2 {
				s.Status = "unmonitored"
			}
		}
		if s.CollectedAt > 0 && now-s.CollectedAt > 180 {
			s.Status = "stale"
		}
		if s.LastSuccess > 0 && now-s.LastSuccess > 180 && s.Status != "unsupported" && s.Status != "unmonitored" {
			s.Status = "stale"
		}
		s.ServerID = server.Id
		s.Name = server.ServerName
		s.GroupID = server.GroupId
		s.ConnectionType = server.ConnectionType
		s.AlertEnabled = !server.MonitorAlertDisabled
		rows = append(rows, s)
	}
	return rows
}

func History(id int, seconds int64) ([]Snapshot, error) {
	if seconds != 3600 && seconds != 86400 && seconds != Retention {
		return nil, fmt.Errorf("invalid history range")
	}
	var records []*models.MonitorSample
	_, err := orm.NewOrm().QueryTable(new(models.MonitorSample)).Filter("server_id", id).Filter("collected_at__gte", time.Now().Unix()-seconds).OrderBy("-collected_at").Limit(4320).All(&records)
	if err != nil {
		return nil, err
	}
	rows := make([]Snapshot, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		var s Snapshot
		if err = json.Unmarshal([]byte(records[i].Data), &s); err != nil {
			return nil, err
		}
		rows = append(rows, s)
	}
	return rows, nil
}

func cleanup(now int64) error {
	// Small batches bound locks and work; no task/log tables are touched.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"server_monitor_sample", "server_monitor_event"} {
		field := "collected_at"
		extra := ""
		if table == "server_monitor_event" {
			field = "closed_at"
			extra = " AND closed_at>0 AND status IN ('sent','failed','cancelled')"
		}
		for i := 0; i < 20; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			name := "`" + strings.ReplaceAll(models.TableName(table), "`", "``") + "`"
			r, err := orm.NewOrm().Raw("DELETE FROM "+name+" WHERE "+field+"<?"+extra+" LIMIT 1000", now-Retention).Exec()
			if err != nil {
				return err
			}
			n, _ := r.RowsAffected()
			if n < 1000 {
				break
			}
		}
	}
	return nil
}
