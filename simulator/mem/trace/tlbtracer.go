// Package trace provides a tlbTracer that can trace memory system tasks.
package trace

import (
	"log"

	"gitlab.com/akita/util/tracing"
)

// A tlbTracer is a hook that can record the actions of a memory model into
// traces.
type tlbTracer struct {
	logger *log.Logger
}

// StartTask marks the start of a memory transaction
func (t *tlbTracer) StartTask(task tracing.Task) {
}

// StepTask marks the memory transaction has completed a milestone
func (t *tlbTracer) StepTask(task tracing.Task) {
	if task.Steps[0].What != "cta-page-map" {
		return
	}

	pair, ok := task.Detail.(struct {
		CtaID int
		Addr  uint64
	})
	if !ok {
		return
	}

	t.logger.Printf("%s, %s, %v, %X\n",
		task.ID,
		task.Steps[0].What,
		pair.CtaID,
		pair.Addr,
	)
}

// EndTask marks the end of a memory transaction
func (t *tlbTracer) EndTask(task tracing.Task) {
}

// NewTracer creates a new Tracer.
func NewTLBTracer(logger *log.Logger) tracing.Tracer {
	t := new(tlbTracer)
	t.logger = logger
	return t
}
