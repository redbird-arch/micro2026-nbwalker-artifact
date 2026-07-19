package tracing

import (
	"sync"

	"gitlab.com/akita/akita"
)

// MMUCacheReqTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type MMUCacheReqTracer struct {
	filter        TaskFilter
	lock          sync.Mutex
	averageTime   akita.VTimeInSec
	inflightTasks map[string]Task
	taskCount     uint64
}

// NewMMUCacheReqTracer creates a new MMUCacheReqTracer
func NewMMUCacheReqTracer(filter TaskFilter) *MMUCacheReqTracer {
	t := &MMUCacheReqTracer{
		filter:        filter,
		inflightTasks: make(map[string]Task),
	}
	return t
}

// AverageTime returns the total time has been spent on a certain type of tasks.
func (t *MMUCacheReqTracer) AverageTime() akita.VTimeInSec {
	t.lock.Lock()
	time := t.averageTime
	t.lock.Unlock()
	return time
}

// TotalCount returns the total number of tasks.
func (t *MMUCacheReqTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// StartTask records the task start time
func (t *MMUCacheReqTracer) StartTask(task Task) {
	if !t.filter(task) {
		return
	}

	t.lock.Lock()
	t.inflightTasks[task.Detail.(string)] = task
	t.lock.Unlock()
}

// StepTask does nothing
func (t *MMUCacheReqTracer) StepTask(task Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *MMUCacheReqTracer) EndTask(task Task) {
	t.lock.Lock()
	originalTask, ok := t.inflightTasks[task.ID]
	if !ok {
		t.lock.Unlock()
		return
	}

	taskTime := task.EndTime - originalTask.StartTime
	t.averageTime = akita.VTimeInSec(
		(float64(t.averageTime)*float64(t.taskCount) + float64(taskTime)) /
			float64(t.taskCount+1))
	delete(t.inflightTasks, task.ID)
	t.taskCount++

	t.lock.Unlock()
}
