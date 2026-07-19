package wavefront

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mgpusim/insts"
)

type ThreadState int

const (
	InValid ThreadState = iota
	Valid
	Walking
	Finished
)

type TranslationThread struct {
	Status          ThreadState
	OriginalRequest *device.TranslationReq

	Level  int
	PPN    uint64
	Offset uint64
	VAddr  uint64

	Page device.Page
}

type TranslationWavefront struct {
	ID        string
	PageTable *device.PageTableImpl

	Threads []*TranslationThread
}

// NewTranslationWavefront creates a new TranslationWavefront
func NewTranslationWavefront() *TranslationWavefront {
	wf := new(TranslationWavefront)
	wf.ID = akita.GetIDGenerator().Generate()

	wf.Threads = make([]*TranslationThread, 0)
	for i := 0; i < 64; i++ {
		wf.Threads = append(wf.Threads, &TranslationThread{
			Status: InValid,
			Level:  0,
		})
	}

	return wf
}

// Accept returns true if the wavefront can accept the translation request
func (wf *TranslationWavefront) Accept(req *device.TranslationReq) bool {
	for _, t := range wf.Threads {
		if t.Status == InValid {
			t.OriginalRequest = req
			t.Status = Valid

			t.VAddr = wf.PageTable.Rearrange(req.VAddr)
			t.PPN = wf.PageTable.GetRoot(req.PID)

			if req.Data != nil {
				data := binary.LittleEndian.Uint64(req.Data)
				t.PPN = data & ^uint64(3)
				level := int(data & uint64(3))
				t.VAddr = wf.PageTable.MoveToLevel(
					t.VAddr,
					level+1,
				)
				t.Level = level + 1
			}

			return true
		}
	}

	return false
}

// Complete
func (wf *TranslationWavefront) Complete() bool {
	ready := false
	for _, t := range wf.Threads {
		switch t.Status {
		case Valid:
			ready = true
		case Walking:
			panic("thread is still walking")
		case Finished:
			t.OriginalRequest = nil
			t.Status = InValid
			t.Level = 0
			t.PPN = 0
			t.VAddr = 0
			t.Page = device.Page{}
		}
	}

	return ready
}

func (wf *TranslationWavefront) HandlePTEDataLoadReturn(
	index int,
	data []byte,
) bool {
	t := wf.Threads[index]
	if t.Status != Walking {
		panic(fmt.Sprintf("thread %d is not walking, but got PTE data load return", index))
	}

	t.VAddr = wf.PageTable.NextLevel(t.VAddr)

	t.PPN = binary.LittleEndian.Uint64(data)

	if t.Level+1 == 4 {
		page, found := wf.PageTable.Find(
			wf.Threads[index].OriginalRequest.PID,
			wf.Threads[index].OriginalRequest.VAddr,
		)
		if !found {
			panic("page not found")
		}

		if page.PAddr != t.PPN {
			panic(fmt.Sprintf("PPN mismatch: expected %#x, got %#x", page.PAddr, t.PPN))
		}

		t.Status = Finished

		t.Page = device.Page{
			PID:   t.OriginalRequest.PID,
			VAddr: t.OriginalRequest.VAddr,
			PAddr: t.PPN,
			Valid: true,
		}

		return true
	}
	t.Level++

	return false
}

func (wf *TranslationWavefront) NextExecUnit(PC uint64) insts.ExeUnit {
	switch PC {
	case 0x0:
		return insts.ExeUnitVALU
	case 0x8:
		return insts.ExeUnitLDS
	case 0x10:
		return insts.ExeUnitVALU
	case 0x18:
		return insts.ExeUnitVALU
	case 0x20:
		return insts.ExeUnitVALU
	case 0x28:
		return insts.ExeUnitVALU
	case 0x30:
		return insts.ExeUnitVALU
	case 0x38:
		return insts.ExeUnitVALU
	case 0x40:
		return insts.ExeUnitVALU
	case 0x48:
		return insts.ExeUnitWalkerScalar
	case 0x50:
		return insts.ExeUnitBranch
	case 0x58:
		return insts.ExeUnitVALU
	case 0x60:
		return insts.ExeUnitVALU
	case 0x68:
		return insts.ExeUnitVALU
	case 0x70:
		return insts.ExeUnitVALU
	case 0x78:
		return insts.ExeUnitVALU
	case 0x80:
		return insts.ExeUnitVALU
	case 0x88:
		return insts.ExeUnitVALU
	case 0x90:
		return insts.ExeUnitWalkerMem // load_PTE
	case 0x98:
		return insts.ExeUnitSpecial
	case 0xa0:
		return insts.ExeUnitWalkerMem // update_PWC
	case 0xa8:
		return insts.ExeUnitVALU
	case 0xb0:
		return insts.ExeUnitBranch
	case 0xb8:
		return insts.ExeUnitWalkerMem // store_PTE
	case 0xc0:
		return insts.ExeUnitSpecial // end
	default:
		panic(fmt.Sprintf("unknown instruction %#X\n", PC))
	}
}

func (wf *TranslationWavefront) GetPTEPhyAddr(
	index int,
) uint64 {
	t := wf.Threads[index]

	return wf.PageTable.AddOffset(t.PPN, t.VAddr)
}

func (wf *TranslationWavefront) AlignToPage(
	addr uint64,
) uint64 {
	return wf.PageTable.AlignToPage(addr)
}
