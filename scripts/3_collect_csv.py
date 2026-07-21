#!/usr/bin/env python3
import os
import shutil
import zipfile
import argparse
import sys

PROJECT_ROOT = os.environ["PROJECT_ROOT"]
WORKTREE_ROOT = os.environ["WORKTREE_ROOT"]

if not PROJECT_ROOT:
    print("[ERROR] Please set the environment variable PROJECT_ROOT to the root directory of the project.")
    sys.exit(1)
    
if not WORKTREE_ROOT:
    print("[ERROR] Please set the environment variable WORKTREE_ROOT to the root directory of the worktree.")
    sys.exit(1)
    
configurations = {
    "Baseline": os.path.join(PROJECT_ROOT, "runs", "baseline"),
    "NBWalker": os.path.join(PROJECT_ROOT, "runs", "nbwalker"),
    "NBWalker+AMR": os.path.join(PROJECT_ROOT, "runs", "nbwalker+amr"),
    "Infinite-Walker": os.path.join(PROJECT_ROOT, "runs", "infinite"),
    "MPW": os.path.join(PROJECT_ROOT, "runs", "mpw"),
    "SoftWalker": os.path.join(PROJECT_ROOT, "runs", "softwalker"),
    "UVM": os.path.join(PROJECT_ROOT, "runs", "uvm"),
    "UVM+NBWalker+AMR": os.path.join(PROJECT_ROOT, "runs", "uvm+nbwalker"),
    "UVM+SnakeByte": os.path.join(PROJECT_ROOT, "runs", "snakebyte"),
    "UVM+SnakeByte+NBWalker+AMR": os.path.join(PROJECT_ROOT, "runs", "snakebyte+nbwalker"),
}

benchmarks = [
    "simpleconvolution",
    "jacobi2d",
    "fastwalshtransform",
    "jacobi1d",
    "shoc-reduction",
    "kmeans",
    "stencil2d",
    "pagerank",
    "matrixtranspose",
    "spmv",
    "gups",
    "gesummv",
]
# =============================================


def collect(csv_dir, source_dir):
    os.makedirs(csv_dir, exist_ok=True)

    for bench in benchmarks:
        metrics_path = os.path.join(source_dir, bench, "metrics.csv")
        if os.path.exists(metrics_path):
            new_name = f"{bench}.csv"
            target_path = os.path.join(csv_dir, new_name)
            shutil.copy(metrics_path, target_path)
            print(f"[OK] Copied: {metrics_path} → {target_path}")
        else:
            print(f"[Skip] {bench}: metrics.csv not found.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Collect metrics.csv from benchmarks and zip them."
    )
    parser.add_argument(
        "--configuration",
        type=str,
        required=True,
        help="Configuration to run",
    )
    args = parser.parse_args()
    
    output_dir = os.path.join(PROJECT_ROOT, "results", args.configuration)
    
    if args.configuration not in configurations:
        print(f"[ERROR] Configuration '{args.configuration}' not found.")
        sys.exit(1)
        
    source_dir = configurations[args.configuration]

    collect(output_dir, source_dir)