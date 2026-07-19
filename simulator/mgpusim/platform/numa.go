package platform

import (
	"log"
	"os"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	memtraces "gitlab.com/akita/mem/trace"
	"gitlab.com/akita/mgpusim/builders"
	"gitlab.com/akita/mgpusim/driver"
)

// NUMAPlatformBuilder can build a platform that equips DisTLBGPU GPU.
type NUMAPlatformBuilder struct {
	CommonPlatformBuilder

	tracePTW bool
}

// MakeNUMAPlatformBuilder creates a EmuBuilder with default parameters.
func MakeNUMAPlatformBuilder() NUMAPlatformBuilder {
	b := NUMAPlatformBuilder{
		CommonPlatformBuilder{
			numGPU:                   1,
			log2PageSize:             uint64(12),
			numCUPerShaderArray:      uint64(4),
			numShaderArrayPerChiplet: uint64(32),
			numMemoryBankPerChiplet:  uint64(64),
			numChiplets:              uint64(1),
			totalMem:                 16 * mem.GB,
			bankSize:                 256 * mem.MB,
			lowAddr:                  4 * mem.GB,
		},
		false,
	}
	return b
}

// WithPTWTracing lets the platform trace ptw operations.
func (b *NUMAPlatformBuilder) WithPTWTracing() {
	b.tracePTW = true
}

func (b NUMAPlatformBuilder) Build() (akita.Engine, *driver.Driver) {
	engine := b.createEngine()

	gpuDriver := driver.NewDriver(engine, b.log2PageSize, b.memAllocatorType)
	gpuBuilder := b.createGPUBuilder(engine, gpuDriver)
	pcieConnector, rootComplexID :=
		b.createConnection(engine, gpuDriver)

	rdmaAddressTable := b.createRDMAAddrTable()

	pmcAddressTable := b.createPMCPageTable()

	b.createGPUs(
		rootComplexID, pcieConnector,
		gpuBuilder, gpuDriver,
		rdmaAddressTable, pmcAddressTable)

	return engine, gpuDriver
}

func (b *NUMAPlatformBuilder) createGPUBuilder(
	engine akita.Engine,
	gpuDriver *driver.Driver,
) builders.Builder {
	gpuBuilder := builders.MakeMGPUSimNUMAGPUBuilder()
	gpuBuilder.WithEngine(engine)
	gpuBuilder.WithNumCUPerShaderArray(int(b.numCUPerShaderArray))
	gpuBuilder.WithNumShaderArrayPerChiplet(int(b.numShaderArrayPerChiplet))
	gpuBuilder.WithNumMemoryBankPerChiplet(int(b.numMemoryBankPerChiplet))
	gpuBuilder.WithNumChiplet(int(b.numChiplets))
	gpuBuilder.WithTotalMem(b.totalMem)
	gpuBuilder.CalculateMemoryParameters()
	gpuBuilder.WithLog2PageSize(b.log2PageSize)
	gpuBuilder.WithPageTable(gpuDriver.PageTable)
	gpuBuilder.WithAlg(b.alg)
	gpuBuilder.WithSchedulingPartition(b.partition)
	gpuBuilder.WithCPU(gpuDriver.CPUSideStorage)

	if b.useTLBMonitor {
		gpuBuilder.WithTLBMonitor()
	}

	if b.useCaPWQMonitor {
		gpuBuilder.WithCaPWQMonitor()

		file, err := os.Create("caPWQ.trace")
		if err != nil {
			panic(err)
		}
		logger := log.New(file, "", 0)
		tracer := memtraces.NewCaPWQTracer(logger)

		gpuBuilder.WithCaPWQTracer(tracer)
	}

	if b.tracePTW {
		file, err := os.Create("ptw.trace")
		if err != nil {
			panic(err)
		}
		logger := log.New(file, "", 0)
		tracer := memtraces.NewPTWTracer(logger)

		gpuBuilder.WithPTWTracer(tracer)
	}

	b.setVisTracer(gpuDriver, gpuBuilder)
	b.setTLBTracer(gpuBuilder)
	b.setMemTracer(gpuBuilder)
	b.setISADebugger(gpuBuilder)

	if b.disableProgressBar {
		gpuBuilder.WithoutProgressBar()
	}

	return gpuBuilder
}
