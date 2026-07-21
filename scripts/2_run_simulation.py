#!/usr/bin/python3
import os
import subprocess
import argparse
from datetime import datetime
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

def run_all_scripts(base_dir, server_mode=False):
    base_dir = os.path.abspath(base_dir)
    cwd = os.getcwd()

    for root, dirs, files in os.walk(base_dir):
        for file in files:
            if file.endswith(".sh"):
                script_path = os.path.join(root, file)
                benchmark_name = os.path.splitext(file)[0]

                print(f"[Running] {benchmark_name} ...")

                os.chdir(root)

                if server_mode:
                    cmd = f"sbatch < {file}"
                else:
                    out_file = f"{benchmark_name}.out"
                    err_file = f"{benchmark_name}.err"
                    cmd = f"bash {file} > {out_file} 2> {err_file} &"

                ret = subprocess.call(cmd, shell=True)

                os.chdir(cwd)

                if ret != 0:
                    print(f"[ERROR] {benchmark_name} failed (exit code {ret})")
                else:
                    print(f"[OK] {benchmark_name} submitted/executed successfully.\n")

    print(f"[DONE] All scripts executed from {base_dir}")

def main():
    parser = argparse.ArgumentParser(description="Run or submit benchmark scripts.")
    parser.add_argument(
        "--configuration",
        type=str,
        required=True,
        help="Configuration to run",
    )
    parser.add_argument(
        "--server",
        action="store_true",
        help="If set, use server mode; otherwise run locally",
    )
    args = parser.parse_args()

    if args.configuration not in configurations:
        print(f"[ERROR] Configuration not found: {args.configuration}")
        return

    base_dir = configurations[args.configuration]
    if not os.path.isdir(base_dir):
        print(f"[ERROR] Directory not found: {base_dir}")
        return

    run_all_scripts(base_dir, server_mode=args.server)

if __name__ == "__main__":
    main()