package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/etcd/mvcc/mvccpb"
	"github.com/gogf/gf/os/glog"

	"go-crontab/common"
)

var GScheduler *scheduler

// 调度器
type scheduler struct {
	JobEventChan      chan *common.JobEvent
	JobPlanTable      map[string]*common.JobSchedulePlan
	JobExecutingTable map[string]*common.JobExecuteStatus
	JobExecuteResult  chan *common.JobExecuteResult
}

// 初始化调度器
func InitScheduler() {
	GScheduler = &scheduler{
		JobEventChan:      make(chan *common.JobEvent, 1000),
		JobPlanTable:      make(map[string]*common.JobSchedulePlan),
		JobExecutingTable: make(map[string]*common.JobExecuteStatus),
		JobExecuteResult:  make(chan *common.JobExecuteResult, 1000),
	}
	go GScheduler.ScheduleLoop()
}

func (s *scheduler) PushJobEvent(event *common.JobEvent) {
	s.JobEventChan <- event
}

// 调度
func (s *scheduler) ScheduleLoop() {
	sleep := s.TrySchedule()
	timer := time.NewTimer(sleep)
	for {
		select {
		// 监听job事件变化
		case event := <-s.JobEventChan:
			s.HandleEvent(event)
		// 监听job执行结果
		case result := <-s.JobExecuteResult:
			s.HandleJobResult(result)
		// 睡眠间隔
		case <-timer.C:
		}
		sleep := s.TrySchedule()
		timer.Reset(sleep)
	}
}

// 处理事件
func (s *scheduler) HandleEvent(event *common.JobEvent) {
	switch event.EventType {
	case mvccpb.PUT:
		// 更新任务
		schedulePlan, err := common.BuildJobPlan(event)
		if err != nil {
			glog.Errorf("build job plan, err: %s", err.Error())
			return
		}
		s.JobPlanTable[event.Job.Name] = schedulePlan
	case mvccpb.DELETE:
		// 删除任务
		if _, ok := s.JobPlanTable[event.Job.Name]; ok {
			delete(s.JobPlanTable, event.Job.Name)
		}
	}
}

// 执行任务
func (s *scheduler) TrySchedule() time.Duration {
	var (
		now      = time.Now()
		nearTime time.Time
	)
	if len(s.JobPlanTable) == 0 {
		return time.Second * 1
	}

	for _, jobPlan := range s.JobPlanTable {
		// 任务执行时间到了，执行任务
		if jobPlan.NextTime.Before(now) || jobPlan.NextTime.Equal(now) {
			// 尝试执行任务
			s.TryRunJob(jobPlan)
			// 更新下次执行时间
			jobPlan.NextTime = jobPlan.Expr.Next(now)
		}

		// 获取下次执行时间最近的任务
		if nearTime.IsZero() || jobPlan.NextTime.Before(nearTime) {
			nearTime = jobPlan.NextTime
		}
	}
	sleep := nearTime.Sub(now)
	return sleep
}

func (s *scheduler) TryRunJob(jobPlan *common.JobSchedulePlan) {
	if _, ok := s.JobExecutingTable[jobPlan.Job.Name]; ok {
		fmt.Printf("任务: %s, 正在执行中... \n", jobPlan.Job.Name)
		return
	}
	// 放入状态表
	s.JobExecutingTable[jobPlan.Job.Name] = &common.JobExecuteStatus{
		Job:      jobPlan.Job,
		PlanTime: jobPlan.NextTime,
		RealTime: time.Now(),
	}
	// 调用执行器执行任务
	GExecutor.ExecuteJob(context.TODO(), jobPlan)
}

func (s *scheduler) PushJobResult(result *common.JobExecuteResult) {
	s.JobExecuteResult <- result
}

func (s *scheduler) HandleJobResult(result *common.JobExecuteResult) {
	// 从执行状态表中删除
	if _, ok := s.JobExecutingTable[result.JobPlan.Job.Name]; ok {
		delete(s.JobExecutingTable, result.JobPlan.Job.Name)
	}

	if result.Err != nil {
		fmt.Printf("执行任务：%s  err: %s", result.JobPlan.Job.Name, result.Err)
		return
	}

	fmt.Printf("执行任务：%s  执行结果: %s", result.JobPlan.Job.Name, string(result.OutPut))
}