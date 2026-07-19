package internal

import (
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
)

type Dispatcher interface {
	Register(port akita.Port)
	Distribute(msg akita.Msg) akita.Port
	Receive(port akita.Port)
}

type RoundRobinDispatcher struct {
	nextPtr int

	ports []akita.Port
}

func (d *RoundRobinDispatcher) Register(port akita.Port) {
	d.ports = append(d.ports, port)
}

func (d *RoundRobinDispatcher) Distribute(msg akita.Msg) akita.Port {
	if len(d.ports) == 0 {
		return nil
	}

	port := d.ports[d.nextPtr%len(d.ports)]
	d.nextPtr++
	return port
}

func (d *RoundRobinDispatcher) Receive(port akita.Port) {
	// Do nothing
}

type LeaseFirstDispatcher struct {
	counter []int

	ports []akita.Port
}

func (d *LeaseFirstDispatcher) Register(port akita.Port) {
	d.ports = append(d.ports, port)
	d.counter = append(d.counter, 0)
}

func (d *LeaseFirstDispatcher) Distribute(msg akita.Msg) akita.Port {
	if len(d.ports) == 0 {
		return nil
	}

	min := d.counter[0]
	minIndex := 0
	for i := 1; i < len(d.counter); i++ {
		if d.counter[i] < min {
			min = d.counter[i]
			minIndex = i
		}
	}

	d.counter[minIndex]++

	return d.ports[minIndex]
}

func (d *LeaseFirstDispatcher) Receive(port akita.Port) {
	for i, p := range d.ports {
		if p == port {
			d.counter[i]--

			if d.counter[i] < 0 {
				panic("counter should not be negative")
			}

			return
		}
	}
}

type BackToSourceDispatcher struct {
	ports []akita.Port
}

func (d *BackToSourceDispatcher) Register(port akita.Port) {
	d.ports = append(d.ports, port)
}

func (d *BackToSourceDispatcher) Distribute(msg akita.Msg) akita.Port {
	if len(d.ports) == 0 {
		return nil
	}

	srcName := msg.Meta().Src.Name()
	GPCID, _ := strconv.Atoi(srcName[20:22])

	return d.ports[GPCID]
}

func (d *BackToSourceDispatcher) Receive(port akita.Port) {
	// Do nothing
}

type InterleavedDispatcher struct {
	ports  []akita.Port
	Offset uint64
}

func (d *InterleavedDispatcher) Register(port akita.Port) {
	d.ports = append(d.ports, port)
}

func (d *InterleavedDispatcher) Distribute(msg akita.Msg) akita.Port {
	if len(d.ports) == 0 {
		return nil
	}

	index := uint64(0)
	address := msg.(*device.TranslationReq).VAddr >> d.Offset
	mask := (uint64(1) << 3) - 1
	for i := 0; i < 4; i++ {
		index = index ^ (address & mask)
		address = address >> 3
	}

	return d.ports[index]
}

func (d *InterleavedDispatcher) Receive(port akita.Port) {
	// Do nothing
}
