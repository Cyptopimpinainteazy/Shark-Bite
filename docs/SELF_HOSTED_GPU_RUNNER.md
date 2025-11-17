# Self-Hosted GPU Runner Setup for SharkPool CI

This document describes how to configure a self-hosted runner with NVIDIA GPU support for the `gpu-runtime` CI job.

Tested on: Ubuntu 22.04 LTS (or later)

## Requirements
- NVIDIA GPU(s) with appropriate drivers (recommended: 515+)
- CUDA Toolkit (nvcc) installed and on PATH (CUDA 11.5 or later recommended)
- Docker + NVIDIA Container Toolkit (`nvidia-docker2` / `nvidia-container-toolkit`) if you use containers for builds
- GitHub self-hosted runner registered with labels: `self-hosted`, `linux`, `x64`, `gpu`

## Basic setup steps
1. Install NVIDIA driver
```bash
sudo apt-get update
sudo apt-get install -y build-essential dkms
# installation method depends on your environment and GPU model—see NVIDIA's official installation guide.
```

2. Install CUDA toolkit and `nvcc`
```bash
# For CUDA 12.2 (example):
sudo apt-get install -y nvidia-cuda-toolkit
export PATH=/usr/local/cuda/bin:$PATH
nvcc --version
```

3. Install Docker & NVIDIA container runtime
```bash
sudo apt-get install -y docker.io
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo apt-key add -
curl -fsSL https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-docker2
sudo systemctl restart docker
```

4. Verify CUDA runtime
```bash
nvidia-smi
nvcc --version
docker run --rm --gpus all nvidia/cuda:12.2.2-base-ubuntu22.04 nvidia-smi
```

5. Add self-hosted runner and register labels
  - Follow GitHub docs: Settings → Actions → Runners → Add runner.
  - When registering, add labels: `self-hosted`, `linux`, `x64`, `gpu`.

## Recommended Runner Configuration
- Hardware: 8+ cores CPU, 16+ GB RAM, 1+ NVIDIA GPU with 8GB+ VRAM
- OS: Ubuntu 22.04 LTS or similar
- Networking: Outbound access for Cargo & Go; optionally allow inbound for Docker images if used

## CI Configuration
- In `.github/workflows/ci.yml`, we added a `gpu-runtime` job that requires a runner with `self-hosted` and `gpu` labels.
- The job validates nvcc and nvidia-smi and executes `tests/ci/test_gpu_runtime.sh`.

## Test your runner
After registering the runner, verify it by running the included test script on the runner machine:

```bash
chmod +x /path/to/repo/tests/ci/test_gpu_runtime.sh
cd /path/to/repo
./tests/ci/test_gpu_runtime.sh
```

The script checks `nvidia-smi` and `nvcc` and runs the `sharkpool-miner` GPU tests.

### Example job snippet for workflow
```yaml
gpu-runtime:
  name: GPU Runtime Tests (self-hosted)
  runs-on: [self-hosted, linux, x64, gpu]
  steps:
    - uses: actions/checkout@v4
    - name: Verify GPU tools and versions
      run: |
        nvidia-smi || true
        nvcc --version || true
    - name: Run GPU CI script
      run: |
        chmod +x ./tests/ci/test_gpu_runtime.sh
        ./tests/ci/test_gpu_runtime.sh
```

## Notes
- A self-hosted runner with GPU access is required to run full GPU tests.
- Keep your CUDA toolchain & drivers updated for kernel compatibility.
- If you prefer a container-based runner, ensure the container has `nvcc` and `nvidia-toolkit` or bind-mount the host's CUDA runtime.
