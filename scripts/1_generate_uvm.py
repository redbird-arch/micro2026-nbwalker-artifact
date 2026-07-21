#!/usr/bin/python3
import os
import subprocess
import argparse
import shutil
import sys

from benchmark import memory_overhead

# ==========================================================
# Configuration
# ==========================================================

PROJECT_ROOT = os.environ["PROJECT_ROOT"]
WORKTREE_ROOT = os.environ["WORKTREE_ROOT"]

if not PROJECT_ROOT:
    print("[ERROR] Please set the environment variable PROJECT_ROOT to the root directory of the project.")
    sys.exit(1)
    
if not WORKTREE_ROOT:
    print("[ERROR] Please set the environment variable WORKTREE_ROOT to the root directory of the worktree.")
    sys.exit(1)

WORKTREE_PATH = os.path.join(WORKTREE_ROOT, "AE-UVM")
MGPUSIM_PATH = f"{WORKTREE_ROOT}/AE-UVM/simulator/mgpusim/"
BENCH_PATH = os.path.join(MGPUSIM_PATH, "samples")

BENCHMARKS = [
    "kmeans",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
]

# ==========================================================
# Compile Phase
# ==========================================================


class Benchmark:
    """Helper class for compiling one benchmark"""

    def __init__(self, name):
        self.name = name
        self.path = os.path.join(BENCH_PATH, name)
        self.binary_path = os.path.join(self.path, name)

    def compile(self):
        with open(os.devnull, "w") as fp:
            p = subprocess.Popen(
                "go build", shell=True, cwd=self.path, stdout=fp, stderr=fp
            )
            p.wait()
        return p.returncode == 0


def clean_all():
    print("[Cleaning all benchmarks...]")
    for name in BENCHMARKS:
        bench = Benchmark(name)
        if os.path.exists(bench.binary_path):
            os.remove(bench.binary_path)
    print("[Clean] All benchmarks cleaned.")


def compile_all():
    print("[Compiling all benchmarks...]")
    failed = []
    for name in BENCHMARKS:
        bench = Benchmark(name)
        ok = bench.compile()
        if not ok:
            failed.append(name)

    if failed:
        print(f"[ERROR] Failed to compile: {', '.join(failed)}")
    else:
        print("[Compile] All benchmarks compiled successfully.")


# ==========================================================
# Runner Script Generation Phase
# ==========================================================

def generate_runners(name, yaml_path=None, use_amr=False):
    base_output_dir = os.path.join(PROJECT_ROOT, "runs", name)
    os.makedirs(base_output_dir, exist_ok=True)

    for benchmark in BENCHMARKS:
        # --- Create benchmark-specific folder ---
        bench_dir = os.path.join(base_output_dir, benchmark)
        os.makedirs(bench_dir, exist_ok=True)

        # --- Move compiled binary ---
        src_bin = os.path.join(BENCH_PATH, benchmark, benchmark)
        dst_bin = os.path.join(bench_dir, benchmark)
        if os.path.exists(src_bin):
            shutil.copy(src_bin, dst_bin)
        else:
            print(f"[WARN] No binary found for {benchmark}")

        # --- Generate runner script ---
        file_path = os.path.join(bench_dir, f"{benchmark}.sh")
        with open(file_path, "w") as f:
            f.write("#!/bin/sh\n")
            f.write("#SBATCH -n 1\n")
            f.write(f"#SBATCH -J {benchmark}\n")
            f.write(f"#SBATCH -o {benchmark}.out\n")
            f.write(f"#SBATCH -e {benchmark}.err\n")
            f.write(f"#SBATCH --mem {str(memory_overhead[benchmark])}\n")
            f.write("set -e\n")

            if benchmark == "gups":
                f.write(f"cp {BENCH_PATH}/gups/starts.bin ./starts.bin\n")

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type numa",
                "-mem-allocator-type demandpaging",
                "-use-unified-memory",
            ]
            
            if use_amr:
                cmd.append("-capwq-monitor ")

            # benchmark-specific parameters
            if benchmark == "fastwalshtransform":
                cmd.append("-length=67108864 ")
            if benchmark == "jacobi1d":
                cmd.append("-n=268435456 -steps=1 ")
            if benchmark == "jacobi2d":
                cmd.append("-n=16384 -steps=1 ")
            if benchmark == "kmeans":
                cmd.append("-points=2097152 -features=16 -clusters=20 -max-iter=1 ")
            if benchmark == "matrixtranspose":
                cmd.append("-width=8192 ")
            if benchmark == "pagerank":
                cmd.append("-node=16384 -sparsity=0.5 -iterations=1 ")
            if benchmark == "simpleconvolution":
                cmd.append("-width=16382 -height=16382 ")
            if benchmark == "shoc-reduction":
                cmd.append("-Size=268435456 -Iterations=2 ")
            if benchmark == "spmv":
                cmd.append("-dim=2097152 -sparsity=0.00001 ")
            if benchmark == "stencil2d":
                cmd.append("-row=8192 -col=8192 ")
            if benchmark == "gesummv":
                cmd.append("-n=8192 ")

            if benchmark == "gups":
                cmd.append("-max-inst 10000000 ")
            if benchmark == "gesummv":
                cmd.append("-max-inst 2000000 ")


            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")

def generate_runners_on_desktop(name, yaml_path=None, use_amr=False):
    base_output_dir = os.path.join(PROJECT_ROOT, "runs", name)
    os.makedirs(base_output_dir, exist_ok=True)

    for benchmark in BENCHMARKS:
        # --- Create benchmark-specific folder ---
        bench_dir = os.path.join(base_output_dir, benchmark)
        os.makedirs(bench_dir, exist_ok=True)

        # --- Move compiled binary ---
        src_bin = os.path.join(BENCH_PATH, benchmark, benchmark)
        dst_bin = os.path.join(bench_dir, benchmark)
        if os.path.exists(src_bin):
            shutil.copy(src_bin, dst_bin)
        else:
            print(f"[WARN] No binary found for {benchmark}")

        # --- Generate runner script ---
        file_path = os.path.join(bench_dir, f"{benchmark}.sh")
        with open(file_path, "w") as f:
            # Normal local execution
            f.write("#!/bin/bash\n")
            f.write("set -e\n")

            if benchmark == "gups":
                f.write(f"cp {BENCH_PATH}/gups/starts.bin ./starts.bin\n")

            cmd = [
                f"./{benchmark}",
                "-timing",
                "-no-progress-bar",
                "-report-all",
                "-scheduling round-robin",
                f"-platform-type numa",
                "-mem-allocator-type demandpaging",
                "-use-unified-memory",
            ]
            
            if use_amr:
                cmd.append("-capwq-monitor ")

            # benchmark-specific parameters
            if benchmark == "fastwalshtransform":
                cmd.append("-length=67108864 ")
            if benchmark == "jacobi1d":
                cmd.append("-n=268435456 -steps=1 ")
            if benchmark == "jacobi2d":
                cmd.append("-n=16384 -steps=1 ")
            if benchmark == "kmeans":
                cmd.append("-points=2097152 -features=16 -clusters=20 -max-iter=1 ")
            if benchmark == "matrixtranspose":
                cmd.append("-width=8192 ")
            if benchmark == "pagerank":
                cmd.append("-node=16384 -sparsity=0.5 -iterations=1 ")
            if benchmark == "simpleconvolution":
                cmd.append("-width=16382 -height=16382 ")
            if benchmark == "shoc-reduction":
                cmd.append("-Size=268435456 -Iterations=2 ")
            if benchmark == "spmv":
                cmd.append("-dim=2097152 -sparsity=0.00001 ")
            if benchmark == "stencil2d":
                cmd.append("-row=8192 -col=8192 ")
            if benchmark == "gesummv":
                cmd.append("-n=8192 ")

            if benchmark == "gups":
                cmd.append("-max-inst 10000000 ")
            if benchmark == "gesummv":
                cmd.append("-max-inst 2000000 ")


            # optional yaml config
            if yaml_path:
                cmd.append(f"-yaml-config-file {yaml_path}")

            f.write(" ".join(cmd) + "\n")
            f.write(f'echo "[Done] {benchmark} finished."\n')

        os.chmod(file_path, 0o755)

    print(f"[OK] Generated run scripts and moved binaries to {base_output_dir}")
        

# ==========================================================
# Main Entry
# ==========================================================


def main():
    parser = argparse.ArgumentParser(
        description="Compile benchmarks and generate run scripts."
    )
    parser.add_argument(
        "--server", action="store_true", help="Enable slurm script mode"
    )
    args = parser.parse_args()

    clean_all()
    compile_all()

    if args.server:
        generate_runners(name="uvm", yaml_path=f"{PROJECT_ROOT}/configs/baselineMMU.yaml", use_amr=False)
        generate_runners(name="uvm+nbwalker", yaml_path=f"{PROJECT_ROOT}/configs/nbwalker.yaml", use_amr=True)
    else:
        generate_runners_on_desktop(name="uvm", yaml_path=f"{PROJECT_ROOT}/configs/baselineMMU.yaml", use_amr=False)
        generate_runners_on_desktop(name="uvm+nbwalker", yaml_path=f"{PROJECT_ROOT}/configs/nbwalker.yaml", use_amr=True)

    clean_all()

if __name__ == "__main__":
    main()