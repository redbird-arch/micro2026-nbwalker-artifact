package dispatching

import (
	"log"

	"gitlab.com/akita/mgpusim/kernels"
	"gitlab.com/akita/mgpusim/protocol"
	"gitlab.com/akita/mgpusim/timing/cp/internal/resource"
)

// distAlgorithm dispatches contiguous flat-ID ranges of work-groups to each CU.
type distAlgorithm struct {
	gridBuilder kernels.GridBuilder
	cuPool      resource.CUResourcePool

	nextCU           int
	numDispatchedWGs int

	perCUListOfWG [][]*kernels.WorkGroup
}

func (a *distAlgorithm) RegisterCU(cu resource.DispatchableCU) {
	a.cuPool.RegisterCU(cu)
}

func (a *distAlgorithm) StartNewKernel(info kernels.KernelLaunchInfo) {
	a.numDispatchedWGs = 0
	a.nextCU = 0
	a.gridBuilder.SetKernel(info)

	numCU := a.cuPool.NumCU()
	a.perCUListOfWG = make([][]*kernels.WorkGroup, numCU)
	for i := 0; i < numCU; i++ {
		a.perCUListOfWG[i] = make([]*kernels.WorkGroup, 0)
	}

	if numCU == 0 {
		return
	}

	numWG := a.gridBuilder.NumWG()
	if numWG == 0 {
		return
	}

	for flatID := 0; ; flatID++ {
		wg := a.gridBuilder.NextWG()
		if wg == nil {
			break
		}

		cuID := flatID * numCU / numWG
		a.perCUListOfWG[cuID] = append(a.perCUListOfWG[cuID], wg)
	}
}

// NumWG returns the number of work-groups in the currently-dispatching
// work-group.
func (a *distAlgorithm) NumWG() int {
	return a.gridBuilder.NumWG()
}

// HasNext check if there are more work-groups to dispatch.
func (a *distAlgorithm) HasNext() bool {
	return a.numDispatchedWGs < a.gridBuilder.NumWG()
}

// Next finds the location to dispatch the next work-group.
func (a *distAlgorithm) Next() (location dispatchLocation) {
	numCU := a.cuPool.NumCU()
	if numCU == 0 {
		return dispatchLocation{}
	}

	startingCU := a.nextCU

	for i := 0; i < numCU; i++ {
		cuID := (startingCU + i) % numCU

		if len(a.perCUListOfWG[cuID]) == 0 {
			continue
		}

		wg := a.perCUListOfWG[cuID][0]
		cu := a.cuPool.GetCU(cuID)

		locations, ok := cu.ReserveResourceForWG(wg)
		if !ok {
			continue
		}

		a.nextCU = (cuID + 1) % numCU
		dispatch := dispatchLocation{
			valid: true,
			cu:    cu.DispatchingPort(),
			cuID:  cuID,
			wg:    wg,
		}
		log.Printf("Dispatching work-group %d to CU %d", wg.FlattenedID(), cuID)
		dispatch.locations =
			make([]protocol.WfDispatchLocation, len(locations))
		for i, localtion := range locations {
			dispatch.locations[i] = protocol.WfDispatchLocation(localtion)
		}

		a.perCUListOfWG[cuID] = a.perCUListOfWG[cuID][1:]
		a.numDispatchedWGs++

		return dispatch
	}

	return dispatchLocation{}
}

// FreeResources marks the dispatched location to be available.
func (a *distAlgorithm) FreeResources(location dispatchLocation) {
	a.cuPool.GetCU(location.cuID).FreeResourcesForWG(location.wg)
}
