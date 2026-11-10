package controllers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/context"
	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/internal/testdb"
	"github.com/george518/PPGo_Job/models"
)

func TestParseTaskSearch(t *testing.T) {
	values := url.Values{"owner_id": {"0"}, "server_id": {"0"}, "task_status": {"0"}, "taskName": {" 商品 "},
		"created_from": {"2026-11-09"}, "created_to": {"2026-11-09"}, "latest_result": {"any_error"}}
	s, err := parseTaskSearch(values, 2, "1,,bad,2,1")
	if err != nil {
		t.Fatal(err)
	}
	if s.OwnerId == nil || *s.OwnerId != 0 || s.ServerId == nil || *s.ServerId != 0 || s.TaskStatus == nil || *s.TaskStatus != 0 || s.TaskName != "商品" {
		t.Fatalf("optional zero values/legacy alias lost: %+v", s)
	}
	if !reflect.DeepEqual(s.AllowedGroupIds, []int{1, 2}) || s.IsAdmin || s.CreatedUntil-s.CreatedFrom != 86400 || time.Unix(s.CreatedFrom, 0).UTC().Hour() != 16 {
		t.Fatalf("scope/date error: %+v", s)
	}
	for _, values := range []url.Values{
		{"page": {"0"}}, {"limit": {"101"}}, {"owner_id": {"-1"}}, {"server_id": {"1 OR 1=1"}},
		{"task_status": {"9"}}, {"latest_result": {"running"}}, {"created_from": {"2026-02-30"}},
		{"created_from": {"2026-11-10"}, "created_to": {"2026-11-09"}},
	} {
		if _, err := parseTaskSearch(values, 1, ""); err == nil {
			t.Errorf("accepted %#v", values)
		}
	}
	if got := taskGroupIDs(" ,bad,-1,"); len(got) != 0 {
		t.Fatalf("invalid grants became %v", got)
	}
	if got := taskGroupIDs("0,1"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("explicit default group lost: %v", got)
	}
}

func TestTaskSearchHTTPIntegration(t *testing.T) {
	_, _ = testdb.Open(t, func() { models.Init(time.Now().Unix()) })
	for _, value := range []interface{}{
		&models.Admin{Id: 1, LoginName: "admin", RealName: "管理员", Status: 1},
		&models.Admin{Id: 2, LoginName: "alice", RealName: "张三", Status: 1},
		&models.Admin{Id: 3, LoginName: "inactive", RealName: "离职用户", Status: 0},
		&models.Group{Id: 1, GroupName: "测试分组", Status: 1},
		&models.Group{Id: 2, GroupName: "隐藏分组", Status: 1},
	} {
		if _, err := orm.NewOrm().Insert(value); err != nil {
			t.Fatal(err)
		}
	}
	visible := &models.Task{Id: 1, GroupId: 1, OwnerId: 2, CreateId: 1, TaskName: "<商品>-采集", ServerIds: "0", CronSpec: "0 0 * * * *", Command: "echo test", Status: 0}
	hidden := &models.Task{Id: 2, GroupId: 2, CreateId: 1, TaskName: "隐藏", ServerIds: "0", CronSpec: "0 0 * * * *", Command: "echo test", Status: 0}
	for _, task := range []*models.Task{visible, hidden} {
		if _, err := models.TaskAdd(task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.TaskLogAdd(&models.TaskLog{TaskId: 1, ServerId: 0, Status: -1, CreateTime: 100}); err != nil {
		t.Fatal(err)
	}
	request := func(action string, values url.Values, user int, groups string) (*TaskController, map[string]interface{}) {
		t.Helper()
		r := httptest.NewRequest("POST", "/task/"+strings.ToLower(action), strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		ctx := context.NewContext()
		ctx.Reset(w, r)
		c := &TaskController{}
		c.Init(ctx, "TaskController", action, c)
		c.userId, c.taskGroups = user, groups
		c.controllerName, c.actionName = "task", strings.ToLower(action)
		c.Data["loginUserId"] = user
		func() {
			defer func() {
				if value := recover(); value != nil && value != beego.ErrAbort {
					panic(value)
				}
			}()
			switch action {
			case "Table":
				c.Table()
			case "AjaxSave":
				c.AjaxSave()
			case "ApiTask":
				c.ApiTask()
			case "AjaxStart":
				c.AjaxStart()
			case "AjaxPause":
				c.AjaxPause()
			case "Copy":
				c.Copy()
			case "Edit":
				c.Edit()
			case "Add":
				c.Add()
			case "List":
				c.List()
			case "AuditList":
				c.AuditList()
			case "Detail":
				c.Detail()
			}
		}()
		var body map[string]interface{}
		if w.Body.Len() > 0 {
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response %s: %v", w.Body.String(), err)
			}
		}
		return c, body
	}
	t.Run("list-envelope-permissions-and-summary", func(t *testing.T) {
		_, body := request("Table", url.Values{"latest_result": {"failed"}, "owner_id": {"2"}}, 2, "1")
		if body["code"] != float64(0) || body["count"] != float64(1) {
			t.Fatalf("%#v", body)
		}
		row := body["data"].([]interface{})[0].(map[string]interface{})
		if row["owner_id"] != float64(2) || row["create_id"] != float64(1) || !strings.Contains(row["task_name"].(string), "&lt;商品&gt;") {
			t.Fatalf("%#v", row)
		}
		if row["latest_execution_summary"].(map[string]interface{})["failed_count"] != float64(1) {
			t.Fatalf("%#v", row)
		}
		for _, groups := range []string{"1", ""} {
			_, body = request("Table", url.Values{"group_id": {"2"}}, 2, groups)
			if body["count"] != float64(0) {
				t.Fatalf("group bypass: %#v", body)
			}
		}
		_, body = request("Table", url.Values{"task_status": {"invalid"}}, 1, "")
		if body["code"] != float64(-1) {
			t.Fatalf("bad parameters: %#v", body)
		}
	})
	saveValues := func(id int) url.Values {
		return url.Values{"id": {strconv.Itoa(id)}, "task_name": {"新任务"}, "group_id": {"1"}, "server_ids": {"0"}, "cron_spec": {"0 0 * * * *"}, "command": {"echo test"}, "is_notify": {"0"}}
	}
	t.Run("owner-create-update-api-and-groups", func(t *testing.T) {
		values := saveValues(0)
		_, body := request("AjaxSave", values, 2, "1")
		if body["status"] != float64(0) {
			t.Fatalf("create: %#v", body)
		}
		created := new(models.Task)
		if err := orm.NewOrm().QueryTable(models.TableName("task")).Filter("task_name", "新任务").OrderBy("-id").One(created); err != nil {
			t.Fatal(err)
		}
		if created.OwnerId != 2 || created.CreateId != 2 || created.Status != 2 {
			t.Fatalf("%+v", created)
		}
		values = saveValues(created.Id)
		values.Set("owner_id", "1")
		_, body = request("AjaxSave", values, 2, "1")
		if body["status"] != float64(0) {
			t.Fatalf("edit: %#v", body)
		}
		updated, _ := models.TaskGetById(created.Id)
		if updated.OwnerId != 1 || updated.CreateId != 2 {
			t.Fatalf("%+v", updated)
		}
		values = saveValues(created.Id)
		_, body = request("ApiTask", values, 0, "") // old API omits owner_id
		updated, _ = models.TaskGetById(created.Id)
		if body["status"] != float64(0) || updated.OwnerId != 1 {
			t.Fatalf("old API: %#v %+v", body, updated)
		}
		values = saveValues(0)
		values.Set("create_id", "2")
		_, body = request("ApiTask", values, 0, "")
		if body["status"] != float64(0) {
			t.Fatalf("old API create: %#v", body)
		}
		apiCreated, _ := models.TaskGetById(int(body["message"].(float64)))
		if apiCreated.OwnerId != 2 {
			t.Fatalf("%+v", apiCreated)
		}
		values = saveValues(1)
		values.Set("owner_id", "3")
		_, body = request("AjaxSave", values, 2, "1")
		if body["status"] != float64(-1) {
			t.Fatalf("inactive assignment: %#v", body)
		}
		values = saveValues(2)
		values.Set("owner_id", "2")
		_, body = request("AjaxSave", values, 2, "1")
		if body["status"] != float64(-1) {
			t.Fatalf("cross-group edit: %#v", body)
		}
		values = saveValues(1)
		values.Set("group_id", "2")
		_, body = request("AjaxSave", values, 2, "1")
		if body["status"] != float64(-1) {
			t.Fatalf("cross-group move: %#v", body)
		}
		visible.Status = 1
		if err := visible.Update("Status"); err != nil {
			t.Fatal(err)
		}
		_, body = request("AjaxSave", saveValues(1), 1, "")
		if body["status"] != float64(-1) {
			t.Fatalf("editing enabled task: %#v", body)
		}
		visible.Status = 0
		if err := visible.Update("Status"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("copy-and-render-pages", func(t *testing.T) {
		if err := beego.AddViewPath("../views"); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"List", "AuditList", "Add", "Edit", "Copy", "Detail"} {
			c, body := request(action, url.Values{"id": {"1"}}, 1, "")
			if body != nil {
				t.Fatalf("%s: %#v", action, body)
			}
			var output bytes.Buffer
			if err := beego.ExecuteViewPathTemplate(&output, "task/"+strings.ToLower(action)+".html", "../views", c.Data); err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			if action == "List" || action == "AuditList" {
				if !strings.Contains(output.String(), "latest_result") || !strings.Contains(output.String(), "task-search-reset") {
					t.Fatalf("%s missing search UI", action)
				}
			} else if action != "Detail" && !strings.Contains(output.String(), "name=\"owner_id\"") {
				t.Fatalf("%s missing owner UI", action)
			}
		}
		visible.OwnerId = 3
		if err := visible.Update("OwnerId"); err != nil {
			t.Fatal(err)
		}
		c, _ := request("Copy", url.Values{"id": {"1"}}, 1, "")
		if c.Data["selectedOwnerId"] != 0 {
			t.Fatalf("inactive owner copied: %#v", c.Data["selectedOwnerId"])
		}
		c, _ = request("Edit", url.Values{"id": {"1"}}, 1, "")
		if c.Data["selectedOwnerId"] != 3 {
			t.Fatal("existing inactive owner lost during edit")
		}
	})
	t.Run("start-pause-preserves-owner-and-results", func(t *testing.T) {
		_, body := request("AjaxStart", url.Values{"id": {"1"}}, 1, "")
		if body["status"] != float64(0) {
			t.Fatalf("start: %#v", body)
		}
		t.Cleanup(func() { request("AjaxPause", url.Values{"id": {"1"}}, 1, "") })
		task, err := models.TaskGetById(1)
		if err != nil || task.Status != 1 || task.OwnerId != 3 {
			t.Fatalf("started task: %+v %v", task, err)
		}
		_, body = request("Table", url.Values{"task_id": {"1"}, "task_status": {"1"}, "latest_result": {"failed"}}, 1, "")
		if body["count"] != float64(1) {
			t.Fatalf("enabled task lost its recorded result: %#v", body)
		}
		// Mainline permits copying an enabled task, but editing still requires a pause.
		copyPage, copyBody := request("Copy", url.Values{"id": {"1"}}, 1, "")
		if copyBody != nil || copyPage.Data["task"].(*models.Task).Id != 1 || copyPage.Data["selectedOwnerId"] != 0 {
			t.Fatalf("copying enabled task: %#v %#v", copyBody, copyPage.Data["selectedOwnerId"])
		}
		copyValues := saveValues(0)
		copyValues.Set("task_name", "enabled task copy")
		copyValues.Set("owner_id", "0")
		_, copyBody = request("AjaxSave", copyValues, 1, "")
		var copied models.Task
		if copyBody["status"] != float64(0) || orm.NewOrm().QueryTable(models.TableName("task")).Filter("task_name", "enabled task copy").One(&copied) != nil || copied.Id == 1 || copied.Status != 0 || copied.OwnerId != 0 {
			t.Fatalf("copy should create a separate paused task: %#v %+v", copyBody, copied)
		}
		source, err := models.TaskGetById(1)
		if err != nil || source.Status != 1 || source.OwnerId != 3 {
			t.Fatalf("copy changed the source task: %+v %v", source, err)
		}
		_, body = request("AjaxPause", url.Values{"id": {"1"}}, 1, "")
		task, err = models.TaskGetById(1)
		if body["status"] != float64(0) || err != nil || task.Status != 0 || task.OwnerId != 3 {
			t.Fatalf("pause: %#v %+v %v", body, task, err)
		}
	})
	t.Run("single-remote-node-next-time", func(t *testing.T) {
		server := &models.TaskServer{ServerName: "scheduled-only fixture", ServerIp: "127.0.0.1", Port: 1, ServerAccount: "fixture", Password: "fixture", Status: 0}
		serverId, err := models.TaskServerAdd(server)
		if err != nil {
			t.Fatal(err)
		}
		task := &models.Task{TaskName: "single remote schedule", GroupId: 1, OwnerId: 2, CreateId: 1, ServerIds: strconv.FormatInt(serverId, 10), CronSpec: "0 0 0 1 1 *", Command: "fixture", Status: 0}
		taskId, err := models.TaskAdd(task)
		if err != nil {
			t.Fatal(err)
		}
		id := strconv.FormatInt(taskId, 10)
		_, body := request("AjaxStart", url.Values{"id": {id}}, 1, "")
		if body["status"] != float64(0) {
			t.Fatalf("start: %#v", body)
		}
		t.Cleanup(func() { request("AjaxPause", url.Values{"id": {id}}, 1, "") })
		_, body = request("Table", url.Values{"task_id": {id}}, 1, "")
		if body["count"] != float64(1) {
			t.Fatalf("query: %#v", body)
		}
		row := body["data"].([]interface{})[0].(map[string]interface{})
		if row["next_time"] == "-" || row["running"] != float64(1) {
			t.Fatalf("single remote node should use its own scheduler key: %#v", row)
		}
	})
}
