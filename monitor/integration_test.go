package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/common"
	"github.com/george518/PPGo_Job/internal/testdb"
	"github.com/george518/PPGo_Job/models"
)

type MetricsFixture struct{}

func (*MetricsFixture) GetMetrics(_ struct{}, m *common.HostMetrics) error {
	*m = common.HostMetrics{Version: 1, BootID: "test", CPUCores: 2, CPUTotal: 1000, CPUIdle: 400, MemoryTotal: 100, MemoryAvailable: 50, DiskTotal: 100, DiskAvailable: 70, DiskPath: "/test"}
	return nil
}

func TestMonitorRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := rpc.NewServer()
	if err = server.RegisterName("RpcTask", new(MetricsFixture)); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	m, err := poll(listener.Addr().String())
	if err != nil || m.Version != 1 || m.DiskPath != "/test" {
		t.Fatal(m, err)
	}
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	go func() {
		conn, err := blocked.Accept()
		if err == nil {
			defer conn.Close()
			time.Sleep(6 * time.Second)
		}
	}()
	start := time.Now()
	if _, err = poll(blocked.Addr().String()); err == nil || time.Since(start) > 6*time.Second {
		t.Fatal("RPC deadline not enforced", err)
	}
}

func TestMonitorIntegration(t *testing.T) {
	db, prefix := testdb.Open(t, func() { models.Init(time.Now().Unix()) })
	o := orm.NewOrm()
	// Recreate only this test's uniquely prefixed monitoring tables with the
	// actual migration, rather than relying on ORM-generated schemas.
	for _, name := range []string{"server_monitor_sample", "server_monitor_config", "server_monitor_state", "server_monitor_event"} {
		if _, err := db.Exec("DROP TABLE `" + prefix + name + "`"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("ALTER TABLE `" + prefix + "task_server` DROP COLUMN monitor_alert_disabled"); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../migrations/20261110_server_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = conn.ExecContext(context.Background(), "SET @ppgo_prefix=?", prefix); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	if err = initialize(); err != nil {
		t.Fatal(err)
	}
	server := &models.TaskServer{Id: 1, ServerName: "test <host>" + strings.Repeat("x", 240), ServerIp: "127.0.0.1", ServerAccount: "test", Password: "test", ConnectionType: 2, Status: 0}
	for _, v := range []interface{}{&models.Admin{Id: 1, Status: 1, Email: "ops@example.invalid"}, server} {
		if _, err = o.Insert(v); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultSettings()
	cfg.Enabled = true
	cfg.RecipientIDs = []int{1}
	if err = SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Unix() - 600
	high := 95.0
	low := 20.0
	feed := func(at int64, value *float64) {
		t.Helper()
		if err := processSample(server, Snapshot{ServerID: 1, CollectedAt: at, Status: "ok", CPU: value}); err != nil {
			t.Fatal(err)
		}
	}
	for at := base; at <= base+300; at += 60 {
		feed(at, &high)
	}
	var events []*models.MonitorEvent
	loadEvents := func() {
		t.Helper()
		if _, err := o.QueryTable(new(models.MonitorEvent)).OrderBy("id").All(&events); err != nil {
			t.Fatal(err)
		}
	}
	loadEvents()
	if len(events) != 1 || events[0].Kind != "firing" {
		t.Fatalf("unexpected firing: %+v", events)
	}
	if err = initialize(); err != nil {
		t.Fatal(err)
	}
	feed(base+360, &high)
	loadEvents()
	if len(events) != 1 {
		t.Fatal("restart duplicated alarm")
	}
	sent := 0
	send := func(to, subject, body string) error {
		sent++
		if to != "ops@example.invalid" || !strings.Contains(body, "test <host>") {
			t.Fatal(to, body)
		}
		return nil
	}
	if err = deliverOne(time.Now().Unix(), send); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatal("not delivered")
	}
	feed(base+420, &low)
	feed(base+480, &low)
	loadEvents()
	if len(events) != 2 || events[1].Kind != "resolved" {
		t.Fatal("recovery not queued")
	}
	if err = deliverOne(time.Now().Unix(), send); err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatal("recovery not sent")
	}
	// New incident remains unsent: failed delivery is retried, then disabling
	// cancels the retry and must not manufacture a recovery notification.
	for at := base + 540; at <= base+840; at += 60 {
		feed(at, &high)
	}
	now := base + 900
	if err = deliverOne(now, func(string, string, string) error { return errors.New("offline") }); err != nil {
		t.Fatal(err)
	}
	loadEvents()
	last := events[len(events)-1]
	if last.Status != "pending" || last.Attempts != 1 {
		t.Fatal("retry not persisted", last)
	}
	if err = SetServerAlert(1, false); err != nil {
		t.Fatal(err)
	}
	loadEvents()
	if events[len(events)-1].Status != "cancelled" {
		t.Fatal("disable didn't cancel")
	}
	// Recovery while an initial send is in flight queues the recovery only after
	// SMTP accepts the first message.
	if err = SetServerAlert(1, true); err != nil {
		t.Fatal(err)
	}
	server.MonitorAlertDisabled = false
	for at := now + 60; at <= now+360; at += 60 {
		feed(at, &high)
	}
	if err = deliverOne(now+400, func(string, string, string) error { feed(now+420, &low); feed(now+480, &low); return nil }); err != nil {
		t.Fatal(err)
	}
	loadEvents()
	if events[len(events)-1].Kind != "resolved" {
		t.Fatal("in-flight recovery lost")
	}
	// Actual read-only Agent RPC -> collection -> database -> history chain.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rpcServer := rpc.NewServer()
	if err = rpcServer.RegisterName("RpcTask", new(MetricsFixture)); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go rpcServer.ServeCodec(jsonrpc.NewServerCodec(c))
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	server.Port, _ = strconv.Atoi(port)
	if _, err = o.Update(server, "Port"); err != nil {
		t.Fatal(err)
	}
	if _, err = o.Insert(&models.Task{Id: 1, Status: 1, ServerIds: "1,1,11"}); err != nil {
		t.Fatal(err)
	}
	current := time.Now().Unix()
	if err = collectCycle(current); err != nil {
		t.Fatal(err)
	}
	rows, err := History(1, 3600)
	if err != nil || len(rows) != 1 || rows[0].Assigned == nil || *rows[0].Assigned != 1 || rows[0].Memory == nil {
		t.Fatal(rows, err)
	}
	old := models.MonitorSample{ServerId: 1, CollectedAt: current - Retention - 1, Data: `{}`}
	if _, err = o.Insert(&old); err != nil {
		t.Fatal(err)
	}
	if err = cleanup(current); err != nil {
		t.Fatal(err)
	}
	if err = o.Read(&old); err != orm.ErrNoRows {
		t.Fatal("old samples not pruned", err)
	}
	var stateRows []*models.MonitorState
	if _, err = o.QueryTable(new(models.MonitorState)).All(&stateRows); err != nil {
		t.Fatal(err)
	}
	for _, r := range stateRows {
		var s AlertState
		if err = json.Unmarshal([]byte(r.Data), &s); err != nil {
			t.Fatal(err)
		}
	}
}
