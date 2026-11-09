package controllers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/astaxie/beego"
	"github.com/george518/PPGo_Job/models"
)

// Empty/malformed grants never become the default group (0).
func taskGroupIDs(raw string) []int {
	ids := make([]int, 0)
	seen := make(map[int]bool)
	for _, value := range strings.Split(raw, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && id >= 0 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}

func (self *TaskController) canAccessTaskGroup(id int) bool {
	if self.userId == 1 {
		return true
	}
	for _, allowed := range taskGroupIDs(self.taskGroups) {
		if allowed == id {
			return true
		}
	}
	return false
}

func (self *TaskController) taskSearchGroups() map[int]string {
	groups := taskGroupLists(self.taskGroups, self.userId)
	for id := range groups {
		if !self.canAccessTaskGroup(id) {
			delete(groups, id)
		}
	}
	return groups
}

func (self *TaskController) prepareTaskSearch(audit bool) {
	self.Data["taskGroup"] = self.taskSearchGroups()
	self.Data["serverGroup"] = serverLists(self.serverGroups, self.userId)
	people, err := models.TaskPeople(true)
	if err != nil {
		beego.Error("task search people: ", err)
		self.Abort("500")
		return
	}
	self.Data["searchPeople"] = people
	self.Data["searchAudit"] = audit
}

func (self *TaskController) prepareTaskOwner(selected int, copying bool) {
	people, err := models.TaskPeople(true)
	if err != nil {
		beego.Error("task owner options: ", err)
		self.Abort("500")
		return
	}
	choices := make([]models.TaskPerson, 0, len(people))
	found := false
	for _, person := range people {
		if person.Active || (!copying && person.Id == selected) {
			choices = append(choices, person)
			if person.Id == selected {
				found = true
			}
		}
	}
	if selected != 0 && !found {
		if copying {
			selected = 0
		} else {
			choices = append(choices, models.TaskPerson{Id: selected, Name: models.TaskPersonDisplay(selected, nil, "未分配")})
		}
	}
	self.Data["ownerOptions"] = choices
	self.Data["selectedOwnerId"] = selected
}

func (self *TaskController) assignTaskOwner(task *models.Task, creating bool) {
	id, err := models.ResolveTaskOwner(self.GetString("owner_id"), creating, task.CreateId, task.OwnerId)
	if err != nil {
		self.ajaxMsg(err.Error(), MSG_ERR)
		return
	}
	task.OwnerId = id
}

func parseTaskSearch(values url.Values, userId int, grants string) (models.TaskSearch, error) {
	s := models.TaskSearch{Page: 1, PageSize: 30, IsAdmin: userId == 1, AllowedGroupIds: taskGroupIDs(grants)}
	optionalInt := func(key string, minimum int) (*int, error) {
		raw := strings.TrimSpace(values.Get(key))
		if raw == "" {
			return nil, nil
		}
		id, err := strconv.Atoi(raw)
		if err != nil || id < minimum {
			return nil, fmt.Errorf("%s 参数无效", key)
		}
		return &id, nil
	}
	for _, item := range []struct {
		key string
		min int
		dst *int
	}{{"page", 1, &s.Page}, {"limit", 1, &s.PageSize}, {"task_id", 1, &s.TaskId}, {"group_id", 0, &s.GroupId}} {
		id, err := optionalInt(item.key, item.min)
		if err != nil {
			return s, err
		}
		if id != nil {
			*item.dst = *id
		}
	}
	if s.PageSize > 100 || s.Page > int(^uint(0)>>1)/s.PageSize {
		return s, fmt.Errorf("分页超出范围，每页最多 100 条")
	}
	for _, item := range []struct {
		key string
		dst **int
	}{{"owner_id", &s.OwnerId}, {"create_id", &s.CreateId}, {"server_id", &s.ServerId}, {"task_status", &s.TaskStatus}} {
		id, err := optionalInt(item.key, 0)
		if err != nil {
			return s, err
		}
		*item.dst = id
	}
	if s.TaskStatus != nil && *s.TaskStatus > 3 {
		return s, fmt.Errorf("任务状态参数无效")
	}
	s.Audit = values.Get("status") == "2"
	s.TaskName = strings.TrimSpace(values.Get("task_name"))
	// Keep old audit-page links usable while both pages now send task_name.
	if s.TaskName == "" {
		s.TaskName = strings.TrimSpace(values.Get("taskName"))
	}
	s.LatestResult = strings.TrimSpace(values.Get("latest_result"))
	if !models.ValidLatestResult(s.LatestResult) {
		return s, fmt.Errorf("最近执行结果参数无效")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return s, err
	}
	for _, item := range []struct {
		key string
		dst *int64
		end bool
	}{{"created_from", &s.CreatedFrom, false}, {"created_to", &s.CreatedUntil, true}} {
		raw := strings.TrimSpace(values.Get(item.key))
		if raw == "" {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", raw, location)
		if err != nil || date.Unix() <= 0 {
			return s, fmt.Errorf("%s 请使用有效日期 YYYY-MM-DD", item.key)
		}
		if item.end {
			date = date.AddDate(0, 0, 1)
		}
		*item.dst = date.Unix()
	}
	if s.CreatedFrom > 0 && s.CreatedUntil > 0 && s.CreatedFrom >= s.CreatedUntil {
		return s, fmt.Errorf("开始日期应早于或等于结束日期")
	}
	return s, nil
}
