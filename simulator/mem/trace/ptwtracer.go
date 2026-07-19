// Package trace provides a tlbTracer that can trace memory system tasks.
package trace

import (
	"log"

	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util/tracing"
)

// A ptwTracer is a hook that can record the actions of a memory model into
// traces.
type ptwTracer struct {
	logger *log.Logger
}

// StartTask marks the start of a memory transaction
func (t *ptwTracer) StartTask(task tracing.Task) {
}

// StepTask marks the memory transaction has completed a milestone
func (t *ptwTracer) StepTask(task tracing.Task) {
	if task.What != "ptw-mem-req" {
		return
	}

	trans, ok := task.Detail.(mmu.Transaction)
	if !ok {
		panic("invalid task detail for ptw-mem-req")
	}

	req := trans.GetMemReq()
	ppn := trans.GetPPN()

	t.logger.Printf("%s, %#x, %#x\n",
		task.Where,
		req.Address,
		ppn,
	)
}

// EndTask marks the end of a memory transaction
func (t *ptwTracer) EndTask(task tracing.Task) {
}

// NewPTWTracer creates a new Tracer.
func NewPTWTracer(logger *log.Logger) tracing.Tracer {
	t := new(ptwTracer)
	t.logger = logger
	return t
}
