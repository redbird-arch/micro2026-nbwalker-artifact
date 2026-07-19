package tracing

import (
	"fmt"
	"strconv"
	"sync"
)

// MaximumCountTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type MaximumCountTracer struct {
	filter   TaskFilter
	lock     sync.Mutex
	maxCount int64

	TracedComponentName string
}

// NewMaximumCountTracer creates a new MaximumCountTracer
func NewMaximumCountTracer(filter TaskFilter) *MaximumCountTracer {
	t := &MaximumCountTracer{
		filter: filter,
	}
	return t
}

// MaximumCount returns the total time has been spent on a certain type of tasks.
func (t *MaximumCountTracer) MaximumCount() int64 {
	t.lock.Lock()
	count := t.maxCount
	t.lock.Unlock()
	return count
}

// StartTask records the task start time
func (t *MaximumCountTracer) StartTask(task Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	count, err := strconv.ParseInt(task.What, 10, 64)
	if err != nil {
		fmt.Println(count, err)
		panic("oh no!")
	}
	if count > t.maxCount {
		t.maxCount = count
	}
	t.lock.Unlock()
}

// StepTask does nothing
func (t *MaximumCountTracer) StepTask(task Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *MaximumCountTracer) EndTask(task Task) {
}
