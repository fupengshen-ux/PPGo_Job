package server

import (
	"github.com/george518/PPGo_Job/common"
	"github.com/george518/PPGo_Job/internal/hostmetrics"
)

func (r *RpcTask) GetMetrics(_ struct{}, reply *common.HostMetrics) error {
	m, err := hostmetrics.Read(C.MonitorDiskPath)
	*reply = m
	return err
}
