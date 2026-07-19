package cache

import (
	"log"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/ca"
)

// NewWalkerMSHR returns a new MSHR object
func NewWalkerMSHR(capacity int) MSHR {
	m := new(walkerMSHR)
	m.capacity = capacity
	return m
}

type walkerMSHR struct {
	*akita.ComponentBase

	capacity int
	entries  []*MSHREntry
}

func (m *walkerMSHR) Add(pid ca.PID, addr uint64) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr && !e.PTW {
			panic("entry already in mshr")
		}
	}

	if len(m.entries) >= m.capacity {
		log.Panic("MSHR is full")
	}

	entry := NewMSHREntry(0)
	entry.PID = pid
	entry.Address = addr
	entry.PTW = false
	m.entries = append(m.entries, entry)
	return entry
}

func (m *walkerMSHR) AddForWalker(pid ca.PID, addr uint64, offset uint64, numSubEntries int) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr && e.PTW {
			panic("entry already in mshr")
		}
	}

	if len(m.entries) >= m.capacity {
		log.Panic("Walker MSHR is full")
	}

	entry := NewMSHREntry(numSubEntries)
	entry.PID = pid
	entry.Address = addr
	entry.PTW = true
	entry.OffsetBits[int(offset)] = true
	m.entries = append(m.entries, entry)
	return entry
}

func (m *walkerMSHR) Query(pid ca.PID, addr uint64) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr && !e.PTW {
			return e
		}
	}
	return nil
}

func (m *walkerMSHR) QueryForWalker(pid ca.PID, addr uint64) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr && e.PTW {
			return e
		}
	}
	return nil
}

func (m *walkerMSHR) Remove(pid ca.PID, addr uint64) *MSHREntry {
	for i, e := range m.entries {
		if e.PID == pid && e.Address == addr && !e.PTW {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return e
		}
	}
	panic("trying to remove an non-exist entry")
}

func (m *walkerMSHR) RemoveForWalker(pid ca.PID, addr uint64) *MSHREntry {
	for i, e := range m.entries {
		if e.PID == pid && e.Address == addr && e.PTW {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return e
		}
	}
	panic("trying to remove an non-exist entry")
}

// AllEntries returns all the MSHREntries that are currently in the walkerMSHR
func (m *walkerMSHR) AllEntries() []*MSHREntry {
	return m.entries
}

// IsFull returns true if no more walkerMSHR entries can be added
func (m *walkerMSHR) IsFull() bool {
	if len(m.entries) >= m.capacity {
		return true
	}
	return false
}

// IsPartialFull returns true if the number of remaining entries
// is less than or equal to the given number
func (m *walkerMSHR) IsPartialFull(remaining int) bool {
	if len(m.entries) >= m.capacity-remaining {
		return true
	}
	return false
}

func (m *walkerMSHR) Reset() {
	m.entries = nil
}
