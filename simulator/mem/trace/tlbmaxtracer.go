package trace

import (
	"sync"

	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/util/tracing"
)

// AverageCountTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type TLBMonitorMaxCountTracer struct {
	filter       tracing.TaskFilter
	lock         sync.Mutex
	maximumCount []float64
	taskCount    uint64

	TracedComponentName string
}

// NewTLBMonitorMaxCountTracer creates a new TLBMonitorMaxCountTracer
func NewTLBMonitorMaxCountTracer(filter tracing.TaskFilter) *TLBMonitorMaxCountTracer {
	t := &TLBMonitorMaxCountTracer{
		filter:       filter,
		maximumCount: make([]float64, 3),
	}
	return t
}

// AverageCount returns the total time has been spent on a certain type of tasks.
func (t *TLBMonitorMaxCountTracer) MaxCounts() []float64 {
	t.lock.Lock()
	counts := t.maximumCount
	t.lock.Unlock()
	return counts
}

// TotalCount returns the total number of tasks.
func (t *TLBMonitorMaxCountTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// StartTask records the task start time
func (t *TLBMonitorMaxCountTracer) StartTask(task tracing.Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	stat, ok := task.Detail.(*monitor.MonitorStats)
	if !ok {
		panic("task detail is not TLBMonitorStat")
	}

	total := stat.Hits + stat.MSHRHits + stat.Misses
	maxTotal := t.maximumCount[0] + t.maximumCount[1] + t.maximumCount[2]
	if float64(total) > maxTotal {
		t.maximumCount[0] = float64(stat.Hits)
		t.maximumCount[1] = float64(stat.MSHRHits)
		t.maximumCount[2] = float64(stat.Misses)
	}

	t.taskCount++

	t.lock.Unlock()
}

// StepTask does nothing
func (t *TLBMonitorMaxCountTracer) StepTask(task tracing.Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *TLBMonitorMaxCountTracer) EndTask(task tracing.Task) {
}
