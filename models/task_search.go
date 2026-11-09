package models

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/astaxie/beego/orm"
)

// TaskSearch is used only by the management lists, not by scheduler startup.
// Nil optional IDs mean "all"; owner 0 means unassigned and server 0 is local.
type TaskSearch struct {
	Page, PageSize  int
	TaskId          int
	TaskName        string
	OwnerId         *int
	CreateId        *int
	GroupId         int
	ServerId        *int
	TaskStatus      *int
	Audit           bool
	IsAdmin         bool
	AllowedGroupIds []int
	CreatedFrom     int64
	CreatedUntil    int64 // exclusive
	LatestResult    string
}

type ExecutionSummary struct {
	TaskId        int   `json:"-"`
	TotalCount    int   `json:"total_count"`
	SuccessCount  int   `json:"success_count"`
	FailedCount   int   `json:"failed_count"`
	TimeoutCount  int   `json:"timeout_count"`
	NoRecordCount int   `json:"no_record_count"`
	LatestTime    int64 `json:"latest_time"`
}

type TaskSearchResult struct {
	Tasks     []*Task
	Count     int64
	Summaries map[int]ExecutionSummary
	People    map[int]string
}

// Only IDs and display names are exposed to search/owner controls.
type TaskPerson struct {
	Id     int
	Name   string
	Active bool
}

func taskPersonName(a *Admin) string {
	name := a.LoginName
	if a.RealName != "" {
		name = a.RealName + " (" + a.LoginName + ")"
	}
	if a.Status != 1 {
		name += "（已停用）"
	}
	return name
}

func TaskPeople(includeInactive bool) ([]TaskPerson, error) {
	query := orm.NewOrm().QueryTable(TableName("uc_admin"))
	if !includeInactive {
		query = query.Filter("status", 1)
	}
	var admins []*Admin
	_, err := query.OrderBy("real_name", "id").Limit(-1).All(&admins, "Id", "LoginName", "RealName", "Status")
	people := make([]TaskPerson, 0, len(admins))
	for _, admin := range admins {
		people = append(people, TaskPerson{Id: admin.Id, Name: taskPersonName(admin), Active: admin.Status == 1})
	}
	return people, err
}

func TaskPersonDisplay(id int, people map[int]string, empty string) string {
	if id == 0 {
		return empty
	}
	if name, ok := people[id]; ok {
		return name
	}
	return fmt.Sprintf("用户 #%d（已删除）", id)
}

// ResolveTaskOwner preserves existing assignments, including inactive users,
// when an old client omits owner_id. New assignments require an active user.
func ResolveTaskOwner(raw string, creating bool, creatorId, previousId int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if !creating {
			return previousId, nil
		}
		if creatorId > 0 {
			a, err := AdminGetById(creatorId)
			if err != nil && err != orm.ErrNoRows {
				return 0, fmt.Errorf("负责人查询失败，请稍后重试")
			}
			if err == nil && a.Status == 1 {
				return creatorId, nil
			}
		}
		return 0, nil
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("负责人参数无效")
	}
	if id == 0 || (!creating && id == previousId) {
		return id, nil
	}
	a, err := AdminGetById(id)
	if err != nil || a.Status != 1 {
		return 0, fmt.Errorf("请选择有效的负责人")
	}
	return id, nil
}

func searchTable(name string) string {
	return "`" + strings.ReplaceAll(TableName(name), "`", "``") + "`"
}

func sqlPlaceholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func (s TaskSearch) where() (string, []interface{}) {
	conditions := []string{"t.status IN (0,1)"}
	if s.Audit {
		conditions[0] = "t.status IN (2,3)"
	}
	args := make([]interface{}, 0)
	if !s.IsAdmin {
		if len(s.AllowedGroupIds) == 0 {
			conditions = append(conditions, "1=0")
		} else {
			conditions = append(conditions, "t.group_id IN ("+sqlPlaceholders(len(s.AllowedGroupIds))+")")
			for _, id := range s.AllowedGroupIds {
				args = append(args, id)
			}
		}
	}
	for _, field := range []struct {
		name string
		id   int
	}{{"id", s.TaskId}, {"group_id", s.GroupId}} {
		if field.id > 0 {
			conditions = append(conditions, "t."+field.name+"=?")
			args = append(args, field.id)
		}
	}
	for _, field := range []struct {
		name string
		id   *int
	}{{"owner_id", s.OwnerId}, {"create_id", s.CreateId}, {"status", s.TaskStatus}} {
		if field.id != nil {
			conditions = append(conditions, "t."+field.name+"=?")
			args = append(args, *field.id)
		}
	}
	if s.TaskName != "" {
		conditions = append(conditions, "LOWER(t.task_name) LIKE LOWER(?) ESCAPE '!' ")
		name := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s.TaskName)
		args = append(args, "%"+name+"%")
	}
	if s.ServerId != nil {
		conditions = append(conditions, "FIND_IN_SET(?, REPLACE(t.server_ids, ' ', '')) > 0")
		args = append(args, strconv.Itoa(*s.ServerId))
	}
	if s.CreatedFrom > 0 {
		conditions = append(conditions, "t.create_time>=?")
		args = append(args, s.CreatedFrom)
	}
	if s.CreatedUntil > 0 {
		conditions = append(conditions, "t.create_time<?")
		args = append(args, s.CreatedUntil)
	}
	return strings.Join(conditions, " AND "), args
}

func latestResultCondition(result string) (string, error) {
	switch result {
	case "", "all":
		return "1=1", nil
	case "success":
		return "s.total_count>0 AND s.success_count=s.total_count", nil
	case "any_error":
		return "s.failed_count+s.timeout_count>0", nil
	case "failed":
		return "s.failed_count>0", nil
	case "timeout":
		return "s.timeout_count>0", nil
	case "partial_missing":
		return "s.no_record_count>0 AND s.no_record_count<s.total_count", nil
	case "no_record":
		return "s.total_count>0 AND s.no_record_count=s.total_count", nil
	default:
		return "", fmt.Errorf("最近执行结果参数无效")
	}
}

func ValidLatestResult(result string) bool {
	_, err := latestResultCondition(result)
	return err == nil
}

const serverTokenCount = "(1+LENGTH(t.server_ids)-LENGTH(REPLACE(t.server_ids,',','')))"

// Expand the existing CSV configuration without a fixed node limit or a new
// relationship table. DISTINCT counts avoid double-counting duplicate IDs.
// The indexed scalar subquery picks a log BEFORE applying any result filter.
// This is compatible with MySQL 5.6 and does not read log output/error bodies.
func executionSummarySQL(where string, maxNodes int, serverId *int) string {
	node := ""
	join := ""
	if serverId != nil {
		node = strconv.Itoa(*serverId)
	} else {
		if maxNodes < 1 {
			maxNodes = 1
		}
		numbers := make([]string, maxNodes)
		for i := range numbers {
			numbers[i] = "SELECT " + strconv.Itoa(i+1) + " AS n"
		}
		token := "TRIM(SUBSTRING_INDEX(SUBSTRING_INDEX(t.server_ids, ',', seq.n), ',', -1))"
		join = " LEFT JOIN (" + strings.Join(numbers, " UNION ALL ") + ") seq ON seq.n<=" + serverTokenCount + " AND " + token + " REGEXP '^[0-9]+$'"
		node = "CAST(" + token + " AS UNSIGNED)"
	}
	return "SELECT t.id AS task_id, COUNT(DISTINCT " + node + ") AS total_count," +
		" COUNT(DISTINCT CASE WHEN l.status=0 THEN " + node + " END) AS success_count," +
		" COUNT(DISTINCT CASE WHEN l.status=-1 THEN " + node + " END) AS failed_count," +
		" COUNT(DISTINCT CASE WHEN l.status=-2 THEN " + node + " END) AS timeout_count," +
		" COUNT(DISTINCT CASE WHEN l.id IS NULL THEN " + node + " END) AS no_record_count," +
		" COALESCE(MAX(l.create_time),0) AS latest_time FROM " + searchTable("task") + " t" + join +
		" LEFT JOIN " + searchTable("task_log") + " l ON l.id=(SELECT recent.id FROM " + searchTable("task_log") +
		" recent WHERE recent.task_id=t.id AND recent.server_id=" + node +
		" ORDER BY recent.create_time DESC,recent.id DESC LIMIT 1) WHERE " + where + " GROUP BY t.id"
}

func TaskSearchList(s TaskSearch) (*TaskSearchResult, error) {
	if s.Page < 1 || s.PageSize < 1 || s.PageSize > 100 || s.Page > int(^uint(0)>>1)/s.PageSize {
		return nil, fmt.Errorf("分页参数无效")
	}
	resultCondition, err := latestResultCondition(s.LatestResult)
	if err != nil {
		return nil, err
	}
	result := &TaskSearchResult{Tasks: make([]*Task, 0), Summaries: make(map[int]ExecutionSummary), People: make(map[int]string)}
	o := orm.NewOrm()
	where, args := s.where()
	from := searchTable("task") + " t"
	if resultCondition != "1=1" {
		maxNodes := 1
		if s.ServerId == nil {
			err = o.Raw("SELECT COALESCE(MAX("+serverTokenCount+"),1) FROM "+from+" WHERE "+where, args...).QueryRow(&maxNodes)
			if err != nil {
				return nil, err
			}
		}
		from += " INNER JOIN (" + executionSummarySQL(where, maxNodes, s.ServerId) + ") s ON s.task_id=t.id"
		where = resultCondition
	}
	if err = o.Raw("SELECT COUNT(*) FROM "+from+" WHERE "+where, args...).QueryRow(&result.Count); err != nil {
		return nil, err
	}
	pageArgs := append(append([]interface{}{}, args...), s.PageSize, (s.Page-1)*s.PageSize)
	_, err = o.Raw("SELECT t.* FROM "+from+" WHERE "+where+" ORDER BY t.status DESC,t.task_name,t.id DESC LIMIT ? OFFSET ?", pageArgs...).QueryRows(&result.Tasks)
	if err != nil || len(result.Tasks) == 0 {
		return result, err
	}

	// For unfiltered lists only the current page needs log lookups. Both paths
	// use the same summary definition; SQL call count is independent of page size.
	ids := make([]interface{}, 0, len(result.Tasks))
	personIds := make(map[int]bool)
	maxNodes := 1
	for _, task := range result.Tasks {
		ids = append(ids, task.Id)
		if n := strings.Count(task.ServerIds, ",") + 1; n > maxNodes {
			maxNodes = n
		}
		if task.CreateId > 0 {
			personIds[task.CreateId] = true
		}
		if task.OwnerId > 0 {
			personIds[task.OwnerId] = true
		}
	}
	var summaries []ExecutionSummary
	_, err = o.Raw(executionSummarySQL("t.id IN ("+sqlPlaceholders(len(ids))+")", maxNodes, s.ServerId), ids...).QueryRows(&summaries)
	if err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		result.Summaries[summary.TaskId] = summary
	}
	if len(personIds) > 0 {
		adminIds := make([]int, 0, len(personIds))
		for id := range personIds {
			adminIds = append(adminIds, id)
		}
		var admins []*Admin
		_, err = o.QueryTable(TableName("uc_admin")).Filter("id__in", adminIds).All(&admins, "Id", "LoginName", "RealName", "Status")
		if err != nil {
			return nil, err
		}
		for _, admin := range admins {
			result.People[admin.Id] = taskPersonName(admin)
		}
	}
	return result, nil
}
