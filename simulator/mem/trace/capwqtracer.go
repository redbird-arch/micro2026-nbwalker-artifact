// Package trace provides a tlbTracer that can trace memory system tasks.
package trace

import (
	"log"

	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/util/tracing"
)

// CaPWQTracer ptwTracer is a hook that can record the actions of a memory model into
// traces.
type CaPWQTracer struct {
	logger *log.Logger
}

// StartTask marks the start of a memory transaction
func (t *CaPWQTracer) StartTask(task tracing.Task) {
	if task.Kind != "CaPWQMonitor" {
		return
	}

	item, ok := task.Detail.(monitor.StatItem)
	if !ok {
		panic("invalid task detail for CaPWQMonitor")
	}

	t.logger.Printf("%v, %v, %v, %v, %v, %v\n",
		item.NumEpoches,
		item.L1VCacheUtilization,
		item.L1VCacheWalkerUtilization,
		item.WalkerReqUtilization,
		item.WalkerRspUtilization,
		item.NumInflightPTW,
	)
}

// StepTask marks the memory transaction has completed a milestone
func (t *CaPWQTracer) StepTask(task tracing.Task) {
}

// EndTask marks the end of a memory transaction
func (t *CaPWQTracer) EndTask(task tracing.Task) {
}

// NewPTWTracer creates a new Tracer.
func NewCaPWQTracer(logger *log.Logger) tracing.Tracer {
	t := new(CaPWQTracer)
	t.logger = logger
	return t
}
