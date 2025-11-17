#!/bin/bash
set -euo pipefail

echo "=== GPU Runtime CI Smoke Test ==="
echo "Checking NVidia devices and CUDA..."

if ! command -v nvidia-smi >/dev/null 2>&1; then
  echo "nvidia-smi not found; please ensure NVIDIA drivers installed on runner"; exit 1
fi
if ! command -v nvcc >/dev/null 2>&1; then
  echo "nvcc not found; please ensure CUDA toolkit is installed on runner"; exit 1
fi

echo "nvidia-smi:"; nvidia-smi || true
echo "nvcc version:"; nvcc --version || true

echo "Running shakedown GPU tests..."

cd $(git rev-parse --show-toplevel)

echo "Building validator-rs with release"; cd validator-rs; cargo build --release; cd -

echo "Building sharkpool-miner with gpu-cuda feature and running gpu tests";
cd sharkpool-miner
export RUST_BACKTRACE=1
# Build with CUDA
cargo test -p sharkpool-miner --features gpu-cuda --release -- --nocapture

echo "GPU runtime smoke tests complete";
