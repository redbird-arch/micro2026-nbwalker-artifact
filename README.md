This repository contains the code for our MICRO 2026 paper:

**NB-Walker: Non-Blocking Page Table Walkers to Enhance Address Translation in NUMA GPUs** </a> <br>
Zihang Chen, Lieven Eeckhout, Hongyuan Liu, Tianao Ge, Xinkai Wang, Jiayi Huang <br>
In Proceedings of 59 ACM/IEEE International Symposium on Microarchitecture <br>

**Requirements:**

- golang 1.23.4

**Building and Running:**

1. Go to mgpusim/samples/\<benchmarkname\>
2. Run `go build` 
3. Run the executable generated with appropriate options (please see scripts/generate_simulations.py for various options).

Please check the scripts directory for helper scripts to automatically compile, copy, and generate runners, and run benchmarks.
