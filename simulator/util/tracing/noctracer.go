package tracing

import (
	"strings"
	"sync"

	"gitlab.com/akita/akita"
)

// NocTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type NocTracer struct {
	filter        TaskFilter
	lock          sync.Mutex
	averageTime   akita.VTimeInSec
	inflightTasks map[string]Task
	taskCount     uint64

	translationAvgTime akita.VTimeInSec
	translationCount   uint64

	dataAvgTime akita.VTimeInSec
	dataCount   uint64
}

// NewTranslationReqTracer creates a new TranslationReqTracer
func NewNocTracer(filter TaskFilter) *NocTracer {
	t := &NocTracer{
		filter:        filter,
		inflightTasks: make(map[string]Task),
	}
	return t
}

// AverageTime returns the total time has been spent on a certain type of tasks.
func (t *NocTracer) AverageTime() akita.VTimeInSec {
	t.lock.Lock()
	time := t.averageTime
	t.lock.Unlock()
	return time
}

// TotalCount returns the total number of tasks.
func (t *NocTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// TranslationAvgTime returns the average time spent on translation requests
func (t *NocTracer) TranslationAvgTime() akita.VTimeInSec {
	t.lock.Lock()
	time := t.translationAvgTime
	t.lock.Unlock()
	return time
}

// DataAvgTime returns the average time spent on data requests
func (t *NocTracer) DataAvgTime() akita.VTimeInSec {
	t.lock.Lock()
	time := t.dataAvgTime
	t.lock.Unlock()
	return time
}

// TranslationCount returns the number of translation requests
func (t *NocTracer) TranslationCount() uint64 {
	t.lock.Lock()
	count := t.translationCount
	t.lock.Unlock()
	return count
}

// DataCount returns the number of data requests
func (t *NocTracer) DataCount() uint64 {
	t.lock.Lock()
	count := t.dataCount
	t.lock.Unlock()
	return count
}

// StartTask records the task start time
func (t *NocTracer) StartTask(task Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	if _, exists := t.inflightTasks[task.ID]; !exists {
		t.inflightTasks[task.ID] = task
	}
	t.lock.Unlock()
}

// StepTask does nothing
func (t *NocTracer) StepTask(task Task) {
}

// EndTask records the end of the task
func (t *NocTracer) EndTask(task Task) {
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

	msg := originalTask.Detail.(akita.Msg)
	if t.isTranslation(msg) {
		t.translationAvgTime = akita.VTimeInSec(
			(float64(t.translationAvgTime)*float64(t.translationCount) +
				float64(taskTime)) /
				float64(t.translationCount+1))
		t.translationCount++
	} else {
		t.dataAvgTime = akita.VTimeInSec(
			(float64(t.dataAvgTime)*float64(t.dataCount) +
				float64(taskTime)) /
				float64(t.dataCount+1))
		t.dataCount++
	}

	delete(t.inflightTasks, task.ID)
	t.taskCount++
	t.lock.Unlock()
}

// isTranslation checks whether the message is a translation request
func (t *NocTracer) isTranslation(msg akita.Msg) bool {
	srcName := msg.Meta().Src.Name()
	dstName := msg.Meta().Dst.Name()

	return strings.Contains(srcName, "TLB") || strings.Contains(dstName, "TLB")
}
