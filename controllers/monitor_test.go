package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"github.com/george518/PPGo_Job/internal/testdb"
	"github.com/george518/PPGo_Job/libs"
	"github.com/george518/PPGo_Job/models"
	"github.com/george518/PPGo_Job/monitor"
)

func TestMonitorHTTPAuthorization(t *testing.T) {
	testdb.Open(t, func() { models.Init(time.Now().Unix()) })
	for _, v := range []interface{}{
		&models.Admin{Id: 1, Status: 1, Password: "password", Salt: "salt", Email: "test@example.invalid"},
		&models.Admin{Id: 2, Status: 1, Password: "password", Salt: "salt", RoleIds: "1"},
		&models.Admin{Id: 3, Status: 0, Password: "password", Salt: "salt", RoleIds: "1"},
		&models.Admin{Id: 4, Status: 1, Password: "password", Salt: "salt", RoleIds: "0"},
		&models.Role{Id: 1, Status: 1, ServerGroupIds: "1,,bad"},
		&models.TaskServer{Id: 1, GroupId: 1, ServerName: "visible", Password: "DO_NOT_EXPOSE", Status: 0},
		&models.TaskServer{Id: 2, GroupId: 2, ServerName: "hidden", Password: "DO_NOT_EXPOSE", Status: 0},
	} {
		if _, err := orm.NewOrm().Insert(v); err != nil {
			t.Fatal(err)
		}
	}
	router := beego.NewControllerRegister()
	router.Add("/server/monitor", &MonitorController{}, "get:Latest")
	router.Add("/server/monitorhistory", &MonitorController{}, "get:History")
	router.Add("/server/monitorsettings", &MonitorController{}, "get:Settings;post:SaveSettings")
	router.Add("/server/monitoralert", &MonitorController{}, "post:ServerAlert")
	request := func(method, path, body string, user int, header bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:4567"
		if user > 0 {
			r.AddCookie(&http.Cookie{Name: "auth", Value: strconv.Itoa(user) + "|" + libs.Md5([]byte("127.0.0.1|passwordsalt"))})
		}
		if header {
			r.Header.Set("X-PPGo-Monitor", "1")
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, user := range []int{0, 3} {
		if w := request("GET", "/server/monitor", "", user, false, ""); w.Code != 401 {
			t.Fatal(user, w.Code, w.Body.String())
		}
	}
	w := request("GET", "/server/monitor", "", 2, false, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden") || strings.Contains(w.Body.String(), "DO_NOT_EXPOSE") {
		t.Fatal(w.Code, w.Body.String())
	}
	var body struct{ Data []monitor.Snapshot }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].ServerID != 1 {
		t.Fatal(body, err)
	}
	w = request("GET", "/server/monitor", "", 4, false, "")
	if !strings.Contains(w.Body.String(), `"data": []`) && !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatal("empty grants leaked servers", w.Body.String())
	}
	if w = request("GET", "/server/monitorhistory?server_id=2&range=1h", "", 2, false, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/server/monitorhistory?server_id=1&range=all", "", 2, false, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/server/monitorsettings", "", 2, false, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	cfg := monitor.DefaultSettings()
	cfg.RecipientIDs = []int{1}
	cfg.Enabled = true
	data, _ := json.Marshal(cfg)
	for _, args := range []struct {
		user   int
		header bool
		origin string
	}{{2, true, ""}, {1, false, ""}, {1, true, "https://evil.invalid"}} {
		if w = request("POST", "/server/monitorsettings", string(data), args.user, args.header, args.origin); w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w = request("POST", "/server/monitorsettings", string(data), 1, true, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("POST", "/server/monitoralert", `{"server_id":1,"enabled":false}`, 1, true, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	server, _ := models.TaskServerGetById(1)
	if !server.MonitorAlertDisabled {
		t.Fatal("switch not persisted")
	}
}
