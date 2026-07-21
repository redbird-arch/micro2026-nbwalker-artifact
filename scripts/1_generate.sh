#!/usr/bin/env bash

python3 $PROJECT_ROOT/scripts/1_generate_nbwalker.py

python3 $PROJECT_ROOT/scripts/1_generate_mpw.py

python3 $PROJECT_ROOT/scripts/1_generate_softwalker.py

python3 $PROJECT_ROOT/scripts/1_generate_uvm.py

python3 $PROJECT_ROOT/scripts/1_generate_snakebyte.py