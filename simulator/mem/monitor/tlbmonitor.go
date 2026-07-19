package monitor

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/tracing"
)

type MonitorComponent interface {
	InitMonitorStats()
	ClearMonitorStats()
	GetMonitorStats() interface{}
	SentCommand(info interface{})
}

type TLBMonitor struct {
	*akita.TickingComponent

	L3TLBs []MonitorComponent

	running     bool
	initialized bool

	numEpoches uint64
}

func NewTLBMonitor(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *TLBMonitor {
	c := &TLBMonitor{}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	return c
}

func (m *TLBMonitor) Tick(now akita.VTimeInSec) bool {
	if !m.running {
		return false
	}

	for _, tlb := range m.L3TLBs {
		m.CollectComponentStats(now, tlb)
	}

	m.numEpoches++

	return true
}

func (m *TLBMonitor) RegisterL3TLB(tlb MonitorComponent) {
	tlb.InitMonitorStats()
	m.L3TLBs = append(m.L3TLBs, tlb)
}

func (m *TLBMonitor) Start(now akita.VTimeInSec) {
	if !m.initialized {
		for _, tlb := range m.L3TLBs {
			tlb.ClearMonitorStats()
		}

		m.initialized = true
	}

	m.running = true

	m.TickLater(now)
}

func (m *TLBMonitor) Stop() {
	for _, tlb := range m.L3TLBs {
		tlb.ClearMonitorStats()
	}

	m.running = false
}

func (m *TLBMonitor) CollectComponentStats(
	now akita.VTimeInSec,
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	tracing.StartTask(
		"",
		"",
		now,
		m,
		"TLBMonitor",
		"",
		component.GetMonitorStats(),
	)

	component.ClearMonitorStats()
}

type MonitorStats struct {
	Name string

	Hits     uint64
	MSHRHits uint64
	Misses   uint64
}

func (stat *MonitorStats) Clear() {
	stat.Hits = 0
	stat.MSHRHits = 0
	stat.Misses = 0
}
