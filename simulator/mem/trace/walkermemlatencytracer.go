package trace

import (
	"sort"
	"sync"

	"gitlab.com/akita/util/tracing"
)

type BinCount struct {
	Bin   uint64
	Count float64
}

type WalkerMemLatencyTracer struct {
	filter        tracing.TaskFilter
	lock          sync.Mutex
	binSize       uint64
	taskCount     uint64
	histogram     map[uint64]float64
	inflightTasks map[string]tracing.Task

	TracedComponentName string
}

// NewTLBMonitorMaxCountTracer creates a new WalkerMemLatencyTracer
func NewWalkerMemLatencyTracer(
	filter tracing.TaskFilter,
	binSize uint64,
) *WalkerMemLatencyTracer {
	t := &WalkerMemLatencyTracer{
		filter:        filter,
		binSize:       binSize,
		histogram:     make(map[uint64]float64),
		inflightTasks: make(map[string]tracing.Task),
	}
	return t
}

func (t *WalkerMemLatencyTracer) Histogram() []BinCount {
	t.lock.Lock()
	pairs := make([]BinCount, 0, len(t.histogram))
	for b, c := range t.histogram {
		pairs = append(pairs, BinCount{Bin: b * t.binSize, Count: c})
	}
	t.lock.Unlock()

	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Bin < pairs[j].Bin })
	return pairs
}

// TotalCount returns the total number of tasks.
func (t *WalkerMemLatencyTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// StartTask records the task start time
func (t *WalkerMemLatencyTracer) StartTask(task tracing.Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	t.inflightTasks[task.ID] = task
	t.lock.Unlock()
}

// StepTask does nothing
func (t *WalkerMemLatencyTracer) StepTask(task tracing.Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *WalkerMemLatencyTracer) EndTask(task tracing.Task) {
	t.lock.Lock()
	originalTask, ok := t.inflightTasks[task.ID]
	if !ok {
		t.lock.Unlock()
		return
	}

	taskTime := (task.EndTime - originalTask.StartTime) * 1e9
	bin := uint64(taskTime) / t.binSize
	t.histogram[bin] += 1
	delete(t.inflightTasks, task.ID)
	t.taskCount++
	t.lock.Unlock()
}
