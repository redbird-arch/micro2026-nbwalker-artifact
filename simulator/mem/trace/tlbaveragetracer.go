package trace

import (
	"sync"

	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/util/tracing"
)

// AverageCountTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type TLBMonitorAverageCountTracer struct {
	filter       tracing.TaskFilter
	lock         sync.Mutex
	averageCount []float64
	taskCount    uint64

	TracedComponentName string
}

// NewTLBMonitorAverageCountTracer creates a new TLBMonitorAverageCountTracer
func NewTLBMonitorAverageCountTracer(filter tracing.TaskFilter) *TLBMonitorAverageCountTracer {
	t := &TLBMonitorAverageCountTracer{
		filter:       filter,
		averageCount: make([]float64, 3),
	}
	return t
}

// AverageCount returns the total time has been spent on a certain type of tasks.
func (t *TLBMonitorAverageCountTracer) AverageCounts() []float64 {
	t.lock.Lock()
	counts := t.averageCount
	t.lock.Unlock()
	return counts
}

// TotalCount returns the total number of tasks.
func (t *TLBMonitorAverageCountTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// StartTask records the task start time
func (t *TLBMonitorAverageCountTracer) StartTask(task tracing.Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	stat, ok := task.Detail.(*monitor.MonitorStats)
	if !ok {
		panic("task detail is not TLBMonitorStat")
	}
	t.averageCount[0] = (t.averageCount[0]*float64(t.taskCount) + float64(stat.Hits)) / (float64(t.taskCount) + 1)
	t.averageCount[1] = (t.averageCount[1]*float64(t.taskCount) + float64(stat.MSHRHits)) / (float64(t.taskCount) + 1)
	t.averageCount[2] = (t.averageCount[2]*float64(t.taskCount) + float64(stat.Misses)) / (float64(t.taskCount) + 1)

	t.taskCount++

	t.lock.Unlock()
}

// StepTask does nothing
func (t *TLBMonitorAverageCountTracer) StepTask(task tracing.Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *TLBMonitorAverageCountTracer) EndTask(task tracing.Task) {
}
