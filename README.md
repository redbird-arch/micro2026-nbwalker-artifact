This repository contains the code for our MICRO 2026 paper:

**NB-Walker: Non-Blocking Page Table Walkers to Enhance Address Translation in NUMA GPUs** </a> <br>
Zihang Chen, Lieven Eeckhout, Hongyuan Liu, Tianao Ge, Xinkai Wang, Jiayi Huang <br>
In Proceedings of 59 ACM/IEEE International Symposium on Microarchitecture <br>

**Requirements:**

- golang 1.25.6

## Artifact evaluation

All implementations and AE scripts are hosted in this GitHub repository. The
`AE` branch is a control-only branch: it contains the unified driver, common
configurations, and generator adapters, but no simulator source tree. All four
simulators are prepared as isolated worktrees from revisions in the same
repository.

### 1. Check and prepare the environment

```bash
./ae doctor
./ae setup
./ae versions
```

NBWalker uses the simulator tree from commit
`8837da7dda5591837877d20d4ad51b042c988aad`. Setup fetches the latest commits
from `AE-SoftWalker`, `AE-UVM`, and `AE-SnakeByte`, then places them under:

```text
.ae/worktrees/
├── nbwalker/
├── softwalker/
├── uvm/
└── snakebyte/
```

Use `./ae setup --no-fetch` to reuse locally available remote-tracking refs.

### 2. Generate tasks

Generators are implementation-specific. The existing main generator creates
four task directories:

```bash
./ae generate main

# Or generate Slurm task scripts:
./ae generate main --server
```

The generated directories are `runs/baseline`, `runs/nbwalker`,
`runs/nbwalker+amr`, and `runs/infinite_walker`. Future implementation-specific
generators can be added under `artifact/generators/` without modifying the
common runner.

### 3. Run a task directory

Run is independent of the implementation and branch that generated the tasks:

```bash
./ae run runs/baseline
./ae run runs/nbwalker
./ae run runs/nbwalker+amr
./ae run runs/infinite_walker

# Submit one task directory to Slurm:
./ae run runs/nbwalker --server
```

### 4. Collect one task directory

```bash
./ae collect runs/baseline
./ae collect runs/nbwalker

# Optionally choose the result archive name:
./ae collect path/to/tasks softwalker
```

Results are written under `results/`. Runtime worktrees and generated data in
`.ae/`, `runs/`, and `results/` are ignored by Git.

Run `./ae --help` for a concise command summary.
