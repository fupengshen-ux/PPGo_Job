package models

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/internal/testdb"
)

func searchInt(n int) *int { return &n }

func TestTaskSearchWhere(t *testing.T) {
	s := TaskSearch{AllowedGroupIds: []int{1}, GroupId: 2, OwnerId: searchInt(0), ServerId: searchInt(1), TaskName: "a_%!"}
	where, args := s.where()
	for _, fragment := range []string{"t.group_id IN (?)", "t.group_id=?", "t.owner_id=?", "FIND_IN_SET", "ESCAPE '!'"} {
		if !strings.Contains(where, fragment) {
			t.Errorf("missing %s in %s", fragment, where)
		}
	}
	if !reflect.DeepEqual(args, []interface{}{1, 2, 0, "%a!_!%!!%", "1"}) {
		t.Fatalf("args: %#v", args)
	}
	where, _ = (TaskSearch{}).where()
	if !strings.Contains(where, "1=0") {
		t.Fatal("empty grants should produce no rows")
	}
	if ValidLatestResult("running") || ValidLatestResult("success OR 1=1") {
		t.Fatal("unexpected result accepted")
	}
}

func TestTaskSearchIntegration(t *testing.T) {
	db, prefix := testdb.Open(t, func() { Init(time.Now().Unix()) })
	exec := func(query string, args ...interface{}) sql.Result {
		t.Helper()
		r, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return r
	}

	// Exercise adding the column, historical default=0, custom prefixes, and reruns.
	exec("ALTER TABLE `" + prefix + "task` DROP COLUMN owner_id")
	migration, err := os.ReadFile("../migrations/20261109_task_search.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		exec("SET @ppgo_prefix='" + prefix + "';\n" + string(migration))
	}
	for _, a := range []*Admin{
		{Id: 1, LoginName: "admin", RealName: "管理员", Status: 1},
		{Id: 2, LoginName: "alice", RealName: "张三", Status: 1},
		{Id: 3, LoginName: "disabled", RealName: "离职用户", Status: 0},
	} {
		if _, err := orm.NewOrm().Insert(a); err != nil {
			t.Fatal(err)
		}
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, location).Unix()
	addTask := func(id, group, owner, creator, status int, servers string, created int64) {
		t.Helper()
		task := &Task{Id: id, GroupId: group, OwnerId: owner, CreateId: creator, Status: status, ServerIds: servers,
			TaskName: fmt.Sprintf("任务 %d", id), CronSpec: "0 0 * * * *", Command: "echo test", CreateTime: created}
		if id == 101 {
			task.TaskName = "商品_A%采集"
		}
		if id == 106 {
			task.ServerType = 1 // polling still summarizes each configured node
		}
		if _, err := TaskAdd(task); err != nil {
			t.Fatal(err)
		}
	}
	addTask(101, 1, 2, 1, 0, "1,11", day)
	addTask(102, 1, 3, 2, 0, "1,11", day+86399)
	addTask(103, 1, 0, 2, 0, "1,11", day+86400)
	addTask(104, 1, 0, 1, 0, "0", day-86400)
	addTask(105, 2, 2, 1, 0, "1", day-86400)
	addTask(106, 1, 2, 1, 0, "1,11", day-86400)
	addTask(107, 1, 2, 1, 0, "1,11", day-86400)
	addTask(108, 1, 2, 2, 2, "11", day-86400)
	addTask(109, 1, 2, 2, 3, "11", day-86400)
	addTask(110, 1, 2, 1, -1, "1", day-86400)
	addTask(111, 1, 2, 1, 1, "1", day-86400)
	addTask(112, 1, 0, 1, 0, "", day-86400)
	log := func(task, server, status int, start int64) int64 {
		t.Helper()
		id, err := TaskLogAdd(&TaskLog{TaskId: task, ServerId: server, Status: status, CreateTime: start})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	log(101, 1, -1, 100)
	newSuccess := log(101, 1, 0, 300)
	log(101, 11, 0, 200)
	log(101, 77, -1, 999) // removed node must not affect the result
	log(102, 1, -1, 300)
	log(102, 11, -2, 300)
	log(103, 1, 0, 100)
	log(105, 1, -1, 300)
	log(106, 1, 0, 400)
	log(106, 1, -1, 200) // older start finishes and inserts later
	log(106, 11, 0, 400)
	log(107, 1, 0, 500)
	log(107, 1, -1, 500) // tied seconds: higher log ID wins
	log(107, 11, 0, 500)
	log(108, 11, -1, 500)
	log(109, 11, -2, 500)
	base := TaskSearch{Page: 1, PageSize: 100, AllowedGroupIds: []int{1}}
	check := func(t *testing.T, s TaskSearch, expected ...int) *TaskSearchResult {
		t.Helper()
		res, err := TaskSearchList(s)
		if err != nil {
			t.Fatal(err)
		}
		actual := make([]int, 0, len(res.Tasks))
		for _, task := range res.Tasks {
			actual = append(actual, task.Id)
		}
		sort.Ints(actual)
		sort.Ints(expected)
		if len(actual) != len(expected) || (len(actual) > 0 && !reflect.DeepEqual(actual, expected)) || res.Count != int64(len(expected)) {
			t.Fatalf("got %v count=%d, want %v", actual, res.Count, expected)
		}
		return res
	}
	t.Run("latest-results", func(t *testing.T) {
		for _, tc := range []struct {
			result string
			ids    []int
		}{{"success", []int{101, 106}}, {"any_error", []int{102, 107}}, {"failed", []int{102, 107}},
			{"timeout", []int{102}}, {"partial_missing", []int{103}}, {"no_record", []int{104, 111}}} {
			t.Run(tc.result, func(t *testing.T) {
				s := base
				s.LatestResult = tc.result
				check(t, s, tc.ids...)
			})
		}
		res := check(t, base, 101, 102, 103, 104, 106, 107, 111, 112)
		if res.Summaries[102].FailedCount != 1 || res.Summaries[102].TimeoutCount != 1 || res.Summaries[103].NoRecordCount != 1 || res.Summaries[104].TotalCount != 1 || res.Summaries[112].TotalCount != 0 {
			t.Fatalf("incorrect summaries: %#v", res.Summaries)
		}
		if !strings.Contains(res.People[3], "已停用") {
			t.Fatal("inactive historical user missing")
		}
	})
	t.Run("combined-filters-and-permissions", func(t *testing.T) {
		s := base
		s.OwnerId, s.CreateId = searchInt(2), searchInt(1)
		s.TaskName, s.LatestResult = "商品_A%", "success"
		check(t, s, 101)
		s.TaskName = "商品XA"
		check(t, s)
		s = base
		s.GroupId, s.LatestResult = 2, "failed"
		check(t, s)
		s.IsAdmin = true
		check(t, s, 105)
		s = base
		s.AllowedGroupIds = nil
		check(t, s)
		s = base
		s.OwnerId = searchInt(0)
		check(t, s, 103, 104, 112)
		s = base
		s.CreatedFrom, s.CreatedUntil = day, day+86400
		check(t, s, 101, 102)
		s = base
		s.TaskStatus = searchInt(0)
		check(t, s, 101, 102, 103, 104, 106, 107, 112)
		s = base
		s.Audit, s.LatestResult = true, "any_error"
		check(t, s, 108, 109)
		s.TaskStatus = searchInt(3)
		check(t, s, 109)
	})
	t.Run("server-scope-and-pagination", func(t *testing.T) {
		s := base
		s.ServerId, s.LatestResult = searchInt(11), "failed"
		check(t, s) // B is timeout and G is success on server 11
		s.LatestResult = "timeout"
		check(t, s, 102)
		s.ServerId, s.LatestResult = searchInt(0), "no_record"
		check(t, s, 104)
		s = base
		s.LatestResult, s.PageSize = "failed", 1
		first, err := TaskSearchList(s)
		if err != nil || first.Count != 2 || len(first.Tasks) != 1 {
			t.Fatalf("first page: %#v, %v", first, err)
		}
		s.Page = 2
		second, err := TaskSearchList(s)
		if err != nil || second.Count != 2 || len(second.Tasks) != 1 || second.Tasks[0].Id == first.Tasks[0].Id {
			t.Fatalf("second page: %#v, %v", second, err)
		}
		s.Page = 3
		third, err := TaskSearchList(s)
		if err != nil || third.Count != 2 || len(third.Tasks) != 0 {
			t.Fatalf("empty page: %#v, %v", third, err)
		}
	})
	t.Run("log-deletion-and-node-changes", func(t *testing.T) {
		exec("DELETE FROM `"+prefix+"task_log` WHERE id=?", newSuccess)
		s := base
		s.TaskId, s.LatestResult = 101, "failed"
		check(t, s, 101)
		exec("UPDATE `" + prefix + "task` SET server_ids='11' WHERE id=107")
		s.TaskId, s.LatestResult = 107, "success"
		check(t, s, 107)
		exec("UPDATE `" + prefix + "task` SET server_ids='11,22,11' WHERE id=107")
		s.LatestResult = "partial_missing"
		res := check(t, s, 107)
		if res.Summaries[107].TotalCount != 2 || res.Summaries[107].NoRecordCount != 1 {
			t.Fatal("duplicate or new nodes miscounted")
		}
	})
	t.Run("owner-compatibility", func(t *testing.T) {
		for _, tc := range []struct {
			raw                     string
			creating                bool
			creator, previous, want int
			bad                     bool
		}{{"", true, 2, 0, 2, false}, {"", false, 1, 3, 3, false}, {"0", true, 1, 0, 0, false},
			{"3", false, 1, 3, 3, false}, {"3", true, 1, 0, 0, true}, {"999", true, 1, 0, 0, true}, {"-1", true, 1, 0, 0, true}} {
			got, err := ResolveTaskOwner(tc.raw, tc.creating, tc.creator, tc.previous)
			if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
				t.Errorf("owner %+v: %d, %v", tc, got, err)
			}
		}
	})
	t.Run("indexed-log-lookup", func(t *testing.T) {
		addTask(500, 1, 2, 1, 0, "0,1,11", day)
		for batch := 0; batch < 40; batch++ {
			values := make([]string, 500)
			for i := range values {
				values[i] = fmt.Sprintf("(500,%d,0,%d,'','',0,'')", []int{0, 1, 11}[i%3], batch*500+i+1)
			}
			exec("INSERT INTO `" + prefix + "task_log` (task_id,server_id,status,create_time,output,error,process_time,server_name) VALUES " + strings.Join(values, ","))
		}
		s := base
		s.TaskId, s.LatestResult = 500, "success"
		started := time.Now()
		check(t, s, 500)
		t.Logf("20,000-log query including count and page: %s", time.Since(started))
		rows, err := db.Query("EXPLAIN SELECT id FROM `" + prefix + "task_log` WHERE task_id=500 AND server_id=11 ORDER BY create_time DESC,id DESC LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, _ := rows.Columns()
		usedIndex := false
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			ptrs := make([]interface{}, len(values))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, name := range columns {
				if name == "key" && values[i].String == "idx_task_log_latest_server" {
					usedIndex = true
				}
			}
		}
		if !usedIndex {
			t.Fatal("latest-node lookup did not use the migration's composite index")
		}
	})
}
