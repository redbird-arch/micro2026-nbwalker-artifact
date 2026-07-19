package main

import (
	"flag"
	"log"

	"gitlab.com/akita/mgpusim/benchmarks/shoc/spmv"
	"gitlab.com/akita/mgpusim/samples/runner"
)

const maxInt32 = int(1<<31 - 1)

// Dim is dimension
var Dim = flag.Int("dim", 128, "The number of rows in the input matrix.")

// Sparsity is sparsity
var Sparsity = flag.Float64("sparsity", 0.01,
	"The ratio between non-zero elements to all the elelements in the matrix")

func main() {
	flag.Parse()

	if *Dim <= 0 || *Dim > maxInt32 {
		log.Fatalf("invalid -dim %d: must be in [1, %d]", *Dim, maxInt32)
	}

	runner := new(runner.Runner).ParseFlag().Init()

	benchmark := spmv.NewBenchmark(runner.GPUDriver)
	benchmark.Dim = int32(*Dim)
	benchmark.Sparsity = *Sparsity

	runner.AddBenchmark(benchmark)

	runner.Run()
}
