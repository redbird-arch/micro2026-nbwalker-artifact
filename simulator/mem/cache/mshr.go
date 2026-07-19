package cache

import (
	"log"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/util/ca"
)

// MSHREntry is an entry in MSHR
type MSHREntry struct {
	PID        ca.PID
	Address    uint64
	Requests   []interface{}
	Block      *Block
	ReadReq    *mem.ReadReq
	DataReady  *mem.DataReadyRsp
	Data       []byte
	PTW        bool
	OffsetBits []bool
}

// NewMSHREntry returns a new MSHR entry object
func NewMSHREntry(numSubEntry int) *MSHREntry {
	e := new(MSHREntry)
	e.Requests = make([]interface{}, 0)
	e.OffsetBits = make([]bool, numSubEntry)
	return e
}

// MSHR is an interface that controls MSHR entries
type MSHR interface {
	Query(pid ca.PID, addr uint64) *MSHREntry
	QueryForWalker(pid ca.PID, addr uint64) *MSHREntry
	Add(pid ca.PID, addr uint64) *MSHREntry
	AddForWalker(pid ca.PID, addr uint64, offset uint64, numSubEntries int) *MSHREntry
	Remove(pid ca.PID, addr uint64) *MSHREntry
	RemoveForWalker(pid ca.PID, addr uint64) *MSHREntry
	AllEntries() []*MSHREntry
	IsFull() bool
	IsPartialFull(remaining int) bool
	Reset()
}

// NewMSHR returns a new MSHR object
func NewMSHR(capacity int) MSHR {
	m := new(mshrImpl)
	m.capacity = capacity
	return m
}

type mshrImpl struct {
	*akita.ComponentBase

	capacity int
	entries  []*MSHREntry
}

func (m *mshrImpl) Add(pid ca.PID, addr uint64) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr {
			panic("entry already in mshr")
		}
	}

	if len(m.entries) >= m.capacity {
		log.Panic("MSHR is full")
	}

	entry := NewMSHREntry(0)
	entry.PID = pid
	entry.Address = addr
	m.entries = append(m.entries, entry)
	return entry
}

func (m *mshrImpl) AddForWalker(pid ca.PID, addr uint64, offset uint64, numSubEntries int) *MSHREntry {
	panic("AddForWalker is not supported in this MSHR")
}

func (m *mshrImpl) Query(pid ca.PID, addr uint64) *MSHREntry {
	for _, e := range m.entries {
		if e.PID == pid && e.Address == addr {
			return e
		}
	}
	return nil
}

func (m *mshrImpl) QueryForWalker(pid ca.PID, addr uint64) *MSHREntry {
	panic("QueryWithOffset is not supported in this MSHR")
}

func (m *mshrImpl) Remove(pid ca.PID, addr uint64) *MSHREntry {
	for i, e := range m.entries {
		if e.PID == pid && e.Address == addr {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return e
		}
	}
	panic("trying to remove an non-exist entry")
}

func (m *mshrImpl) RemoveForWalker(pid ca.PID, addr uint64) *MSHREntry {
	panic("RemoveForWalker is not supported in this MSHR")
}

// AllEntries returns all the MSHREntries that are currently in the MSHR
func (m *mshrImpl) AllEntries() []*MSHREntry {
	return m.entries
}

// IsFull returns true if no more MSHR entries can be added
func (m *mshrImpl) IsFull() bool {
	if len(m.entries) >= m.capacity {
		return true
	}
	return false
}

// IsPartialFull returns true if the number of remaining entries
// is less than or equal to the given number
func (m *mshrImpl) IsPartialFull(remaining int) bool {
	if len(m.entries) >= m.capacity-remaining {
		return true
	}
	return false
}

func (m *mshrImpl) Reset() {
	m.entries = nil
}
