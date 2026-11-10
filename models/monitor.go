package models

// Monitoring tables are separate from task execution and log tables.
type MonitorSample struct {
	Id          int64
	ServerId    int
	CollectedAt int64  `orm:"index"`
	Data        string `orm:"type(text)"`
}

func (*MonitorSample) TableName() string       { return TableName("server_monitor_sample") }
func (*MonitorSample) TableUnique() [][]string { return [][]string{{"ServerId", "CollectedAt"}} }

type MonitorConfig struct {
	Id   int
	Data string `orm:"type(text)"`
}

func (*MonitorConfig) TableName() string { return TableName("server_monitor_config") }

type MonitorState struct {
	Id       int64
	ServerId int
	Metric   string `orm:"size(16)"`
	Data     string `orm:"type(text)"`
}

func (*MonitorState) TableName() string       { return TableName("server_monitor_state") }
func (*MonitorState) TableUnique() [][]string { return [][]string{{"ServerId", "Metric"}} }

// One durable notification per incident, transition and recipient.
type MonitorEvent struct {
	Id               int64
	ServerId         int    `orm:"index"`
	Incident         string `orm:"size(48)"`
	Kind             string `orm:"size(16)"`
	Metric           string `orm:"size(16)"`
	Recipient        string `orm:"size(254)"`
	RecipientKey     string `orm:"size(64)"`
	CreatedAt        int64
	ClosedAt         int64  `orm:"index"`
	Status           string `orm:"size(16)"`
	Attempts         int
	NextAttempt      int64 `orm:"index"`
	SuppressRecovery bool
	Subject          string `orm:"size(255)"`
	Body             string `orm:"type(text)"`
	LastError        string `orm:"size(255)"`
}

func (*MonitorEvent) TableName() string { return TableName("server_monitor_event") }
func (*MonitorEvent) TableUnique() [][]string {
	return [][]string{{"Incident", "Kind", "RecipientKey"}}
}
