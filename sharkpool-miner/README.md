# SharkPool Miner

<div align="center">

![SharkPool Miner](https://img.shields.io/badge/SharkPool-Miner-blue?style=for-the-badge)
![Rust](https://img.shields.io/badge/Rust-1.70+-orange?style=for-the-badge&logo=rust)
![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)

**High-Performance GPU Mining Client for Ironfish & Multiple Algorithms**

[Features](#features) • [Quick Start](#quick-start) • [GUI Guide](#gui-application) • [CLI Guide](#cli-usage) • [Building](#building) • [Contributing](#contributing)

</div>

---

## 🚀 Features

### 🖥️ **Dual Interface**
- **Modern GUI**: Cross-platform graphical interface built with egui
- **CLI Interface**: Lightweight command-line tool for automation
- **Both interfaces share the same powerful mining engine**

### 🎮 **Multi-GPU Support**
- **CUDA**: NVIDIA GPUs with optimized kernels (RTX 20/30/40 series)
- **OpenCL**: Multi-vendor GPU support (AMD, Intel, NVIDIA)
- **Automatic GPU Detection**: Auto-configures optimal settings
- **GPU Monitoring**: Real-time temperature, power, utilization tracking

### ⚙️ **Mining Algorithms**
- **Fishhash**: Keccak-256 optimized for Ironfish mining (see GPU Mining below)
- **SHA256d**: Double SHA-256 for Bitcoin-style mining
- **Blake3**: Modern high-performance hash function
- **Argon2id**: Memory-hard password hashing function

### 🎮 **GPU Mining - Fishhash**

**⚠️ IMPORTANT**: The current Fishhash implementation in SharkPool Miner is a **simplified version** using only Keccak-256 hashing, designed for SharkPool's multi-algorithm test environment.

**This implementation is NOT compatible with production Ironfish mining**, which requires the full memory-hard Fishhash algorithm with 4.5GB DAG generation and 32-iteration Blake3 loops.

#### **Requirements**
- **NVIDIA GPUs**: CUDA 11.0+ (RTX 20/30/40 series recommended)
- **AMD/Intel GPUs**: OpenCL 1.2+ compatible hardware
- **GPU Memory**: Minimum 4GB VRAM (6GB+ recommended)

#### **Build Instructions**
```bash
# NVIDIA CUDA support
cargo build --release --features gpu-cuda

# AMD/Intel OpenCL support
cargo build --release --features gpu-opencl

# Both backends
cargo build --release --features gpu-cuda,gpu-opencl
```

#### **Expected Performance**
- **RTX 3070**: >1 GH/s (1 billion hashes/second)
- **RTX 3080**: >1.5 GH/s
- **RTX 3090**: >2 GH/s
- **AMD RX 6800 XT**: ~900 MH/s

#### **Full Fishhash Implementation**
For production Ironfish mining, users should use established miners like **lolMiner** or **Rigel**.

See `FISHHASH_ROADMAP.md` for details on implementing full memory-hard Fishhash in SharkPool Miner (4.5GB DAG + Blake3 iterations).

#### **Testing**
```bash
# Run Fishhash GPU tests
cargo test --features gpu-cuda test_fishhash_ -- --nocapture

# Validation tests
cargo test --features gpu-cuda fishhash_validation -- --nocapture

# Integration tests (performance/benchmarking)
cargo test --features gpu-cuda fishhash_integration -- --nocapture
```

### 🌐 **Pool Integration**
- **Stratum V1/V2**: Full stratum protocol support
- **Multiple Algorithms**: Switch algorithms per pool
- **Job Management**: Intelligent job caching and validation
- **Share Tracking**: Real-time acceptance/rejection monitoring

### 📊 **Monitoring & Metrics**
- **Real-time Dashboard**: Live hashrate charts and statistics
- **GPU Statistics**: Per-GPU temperature, power, utilization
- **Share Analytics**: Acceptance rates and performance metrics
- **Prometheus Metrics**: `/metrics` endpoint for monitoring
- **Logging**: Comprehensive logging with configurable levels

### 🔧 **Performance Optimization**
- **Dynamic Tuning**: Automatic batch size and block size optimization
- **Memory Management**: Efficient GPU memory utilization
- **Thermal Protection**: Automatic shutdown on overheating
- **Power Monitoring**: Real-time power consumption tracking

---

## ⚡ Quick Start

### GUI Application (Recommended)

**Build and run the GUI:**
```bash
# Build with GUI support
cargo build --release --features gui

# Run the GUI
./target/release/sharkpool-miner-gui
```

**Quick Configuration:**
1. Enter your Ironfish wallet address
2. Set pool URL (default: `pool.sharkpool.io:3333`)
3. Select your GPUs
4. Choose Fishhash algorithm (default for Ironfish)
5. Click "Start Mining"

### CLI Usage

**Basic Ironfish Mining:**
```bash
# Build CLI version
cargo build --release

# Start mining
./target/release/sharkpool-miner \
  --pool pool.sharkpool.io:3333 \
  --wallet YOUR_IRONFISH_WALLET \
  --worker rig1 \
  --algorithm fishhash \
  --gpu cuda
```

**Advanced Configuration:**
```bash
./target/release/sharkpool-miner \
  --pool pool.sharkpool.io:3333 \
  --wallet YOUR_WALLET.WORKER \
  --algorithm fishhash \
  --backend cuda \
  --batch-size 1000000 \
  --block-size 256 \
  --gpu-devices 0,1,2 \
  --stats-interval 30
```

---

## 🖥️ GUI Application

The SharkPool Miner GUI provides a modern, intuitive interface for mining operations:

### **Main Dashboard**
- **Real-time Hashrate Charts**: Visual hashrate history with 5-minute window
- **Mining Statistics**: Total hashrate, uptime, shares found/accepted
- **Pool Connection Status**: Live connection monitoring
- **Activity Log**: Real-time mining events and notifications

### **Configuration Panel**
- **Pool Settings**: Pool URL, wallet address, worker name
- **Algorithm Selection**: Fishhash, SHA256d, Blake3, Argon2id
- **GPU Backend**: CUDA or OpenCL selection
- **GPU Device Selection**: Choose which GPUs to use
- **Performance Tuning**: Batch size and block size optimization
- **Presets**: High Performance, Balanced, Low VRAM configurations

### **GPU Statistics**
- **Per-GPU Cards**: Individual cards for each GPU
- **Hardware Metrics**: Temperature, power usage, utilization
- **Performance Indicators**: Color-coded performance status
- **Memory Usage**: VRAM utilization with progress bars
- **Share Tracking**: Individual GPU share statistics

### **Mining Controls**
- **One-Click Mining**: Start/Stop/Restart buttons
- **Status Indicators**: Mining and pool connection status
- **Quick Actions**: View logs, open metrics, copy worker ID
- **Keyboard Shortcuts**: Ctrl+S (Start), Ctrl+Q (Stop), Ctrl+R (Restart)

### **GUI Features**
- **Dark Theme**: SharkPool blue/cyan branding
- **Responsive Layout**: Adapts to different screen sizes
- **Real-time Updates**: 100ms refresh rate for smooth experience
- **Error Handling**: User-friendly error messages and recovery options

---

## 🌊 Unified Pool & Miner GUI

The unified GUI manages both the mining pool server and miner in a single interface.

### Building
```bash
cargo build --release --features gui --bin sharkpool-unified-gui
```

### Running
```bash
./target/release/sharkpool-unified-gui
```

### Features
- **Pool Control Panel**: Start/stop pool server, configure addresses and database connections
- **Pool Stats Panel**: Monitor connected miners, pool hashrate, share statistics, recent jobs
- **Miner Control Panel**: Configure and control GPU mining (same as standalone miner GUI)
- **Miner Stats Panel**: Monitor GPU performance, hashrate, shares (same as standalone miner GUI)

### Requirements
- Pool binary must be built: `cd backend/pool && go build -o mining-pool cmd/mining-pool/main.go`
- Redis server running on localhost:6379 (or configured address)
- PostgreSQL database running with `sharkpool` database created
- CUDA 11.0+ or OpenCL 1.2+ for GPU mining

### Configuration
The unified GUI stores configuration in `~/.sharkpool/unified_config.toml` including:
- Pool server settings (addresses, database connections)
- Miner settings (pool URL, wallet, GPU selection)
- UI preferences (active tab, window size)

### Troubleshooting
- **Pool fails to start**: Check that ports 3333 (stratum) and 9100 (metrics) are available
- **Database connection errors**: Verify Redis and PostgreSQL are running and accessible
- **Miner connection fails**: Ensure pool is started before starting miner

---

## 🛠️ CLI Usage

### **Command Line Options**
```bash
# Required arguments
--pool <URL>              Mining pool address (e.g., pool.sharkpool.io:3333)
--wallet <ADDRESS>        Wallet address (e.g., wallet.worker)

# Optional arguments
--worker <NAME>           Worker name (default: worker1)
--password <PASS>         Pool password (default: x)
--algorithm <ALGO>        Mining algorithm (fishhash, sha256d, blake3, argon2id)
--backend <BACKEND>       GPU backend (cuda, opencl)
--gpu-devices <IDS>       Comma-separated GPU device IDs (e.g., 0,1,2)
--batch-size <SIZE>       Nonces per kernel launch (default: 1000000)
--block-size <SIZE>       Threads per block (default: 256)
--stats-interval <SEC>    Stats reporting interval (default: 30)
--metrics-port <PORT>     Prometheus metrics port (default: 9090)
--log-level <LEVEL>       Logging level (error, warn, info, debug, trace)
--help                    Show help message
```

### **Examples**

**Single GPU Ironfish Mining:**
```bash
./target/release/sharkpool-miner \
  --pool pool.sharkpool.io:3333 \
  --wallet iron1abc123... \
  --worker gpu-rig-1 \
  --algorithm fishhash
```

**Multi-GPU High Performance:**
```bash
./target/release/sharkpool-miner \
  --pool pool.sharkpool.io:3333 \
  --wallet iron1abc123... \
  --worker multi-gpu \
  --algorithm fishhash \
  --backend cuda \
  --gpu-devices 0,1,2,3 \
  --batch-size 5000000 \
  --block-size 512
```

**AMD OpenCL Mining:**
```bash
./target/release/sharkpool-miner \
  --pool pool.sharkpool.io:3333 \
  --wallet iron1abc123... \
  --worker amd-miner \
  --algorithm fishhash \
  --backend opencl \
  --gpu-devices 0,1
```

---

## 🏗️ Building

### **Prerequisites**
- **Rust**: 1.70+ 
- **CUDA Toolkit**: 11.0+ (for NVIDIA GPU mining)
- **GPU Drivers**: Latest drivers for your GPU vendor

### **Platform Support**
| Platform      | GUI | CUDA | OpenCL | Status            |
| ------------- | --- | ---- | ------ | ----------------- |
| Windows 10/11 | ✅   | ✅    | ✅      | Fully Supported   |
| Ubuntu 20.04+ | ✅   | ✅    | ✅      | Fully Supported   |
| macOS 12.0+   | ✅   | ⚠️    | ✅      | GUI + OpenCL only |

### **Build Commands**

**Development Build (GUI + CLI):**
```bash
# Clone repository
git clone https://github.com/sharkpool/miner.git
cd miner

# Build with GUI and CUDA support
cargo build --features gui,gpu-cuda

# Or build CLI only
cargo build --features gpu-cuda
```

**Release Build:**
```bash
# Optimized release build
cargo build --release --features gui,gpu-cuda

# Strip binary for smaller size
strip target/release/sharkpool-miner-gui
strip target/release/sharkpool-miner
```

**Feature Combinations:**
```bash
# GUI + CUDA (Recommended)
cargo build --features gui,gpu-cuda

# GUI + OpenCL (AMD/Intel)
cargo build --features gui,gpu-opencl

# GUI + Both GPU backends
cargo build --features gui,gpu-cuda,gpu-opencl

# CLI + CUDA only
cargo build --features gpu-cuda

# Minimal CLI (no GPU)
cargo build --no-default-features

> Tip: The default Cargo configuration for this project enables the `gui` feature. For headless/CI builds or to skip GUI dependencies, use `cargo build --no-default-features`.

### GPU CI Note
In CI we use a self-hosted GPU runner to execute runtime GPU tests. If you plan to add a runner, follow the instructions in `docs/SELF_HOSTED_GPU_RUNNER.md` and tag the runner with labels `self-hosted`, `linux`, `x64`, and `gpu`.

### CUDA & OpenCL: PTX / Build-time Notes ⚠️

- The build script attempts to compile CUDA kernels using `nvcc` during `cargo build` when the `gpu-cuda` feature is enabled.
- If `nvcc` is not found or compilation fails, the build script sets an environment sentinel `KERNEL_*_PTX_PATH=MISSING_PTX_FILE` for each kernel. At runtime, the `CudaKernelExecutor` checks for this sentinel and returns a clear error instructing you to either install the CUDA toolkit (nvcc) or provide prebuilt PTX files.
- If you see an error complaining about `MISSING_PTX_FILE`, install `nvcc` and ensure `$PATH` includes it, or run `cargo build --features gpu-cuda` on a machine with CUDA toolchain installed.

Install nvcc on Debian/Ubuntu (example):
```bash
sudo apt update
sudo apt install -y nvidia-cuda-toolkit clinfo
export CUDA_ROOT=/usr/local/cuda
export PATH="$CUDA_ROOT/bin:$PATH"
nvcc --version
```

OpenCL: OpenCL kernels are compiled at runtime using the OpenCL platform. Ensure OpenCL ICD and vendor runtime are installed (e.g., AMD ROCm, Intel OpenCL runtime, or NVIDIA OpenCL). Use `clinfo` to check runtimes.

If you prefer to avoid building GPU kernels during CI, build the project with: `cargo build --no-default-features` or disable the `gpu-cuda`/`gpu-opencl` features.

```

### **Cross-Compilation**

**Build for different target:**
```bash
# Linux x86_64
cargo build --target x86_64-unknown-linux-gnu

# Windows x86_64
cargo build --target x86_64-pc-windows-msvc

# macOS
cargo build --target x86_64-apple-darwin
```

---

## 📦 Docker

**Run with Docker:**
```bash
# GPU-enabled container
docker run --gpus all \
  -v /dev/dri:/dev/dri \
  sharkpool/miner:latest \
  --pool pool.sharkpool.io:3333 \
  --wallet YOUR_WALLET \
  --algorithm fishhash
```

**Build Docker Image:**
```bash
# Build image
docker build -t sharkpool-miner .

# Run with GPU support
docker run --gpus all -it sharkpool-miner
```

---

## 📊 Monitoring

### **Prometheus Metrics**
Access metrics at: `http://localhost:9090/metrics`

**Key Metrics:**
- `sharkpool_hashrate_total` - Total hashrate (H/s)
- `sharkpool_shares_total` - Total shares found/accepted/rejected
- `sharkpool_gpu_temperature` - GPU temperatures
- `sharkpool_gpu_power_usage` - GPU power consumption
- `sharkpool_mining_uptime` - Mining uptime seconds

### **Log Monitoring**
```bash
# Follow logs in real-time
tail -f ~/.sharkpool-miner/logs/miner.log

# Check error logs
grep ERROR ~/.sharkpool-miner/logs/miner.log
```

---

## ⚙️ Configuration

### **Config File**
Save configuration to `~/.sharkpool-miner/config.toml`:
```toml
[pool]
url = "pool.sharkpool.io:3333"
wallet = "YOUR_WALLET_ADDRESS"
worker = "rig1"
password = "x"

[mining]
algorithm = "fishhash"
backend = "cuda"
gpu_devices = [0, 1, 2]
batch_size = 1000000
block_size = 256

[monitoring]
stats_interval = 30
metrics_port = 9090
```

### **Environment Variables**
```bash
# Override default pool
export SHARKPOOL_POOL="pool.sharkpool.io:3333"
export SHARKPOOL_WALLET="YOUR_WALLET"

# Debug logging
export RUST_LOG=debug

# CUDA specific
export CUDA_VISIBLE_DEVICES=0,1
```

---

## 🔧 Troubleshooting

### **Common Issues**

**GPU Not Detected:**
```bash
# Check GPU status
nvidia-smi  # NVIDIA
clinfo      # OpenCL

# List available GPUs
cargo run --bin sharkpool-miner -- --list-gpus
```

**High Temperature Warnings:**
- Reduce batch size in GUI config panel
- Enable fan curve optimization in GPU software
- Ensure adequate case ventilation

**Low Hashrate:**
- Try different batch/block size combinations
- Update GPU drivers
- Check GPU thermal throttling

**Pool Connection Issues:**
- Verify pool URL and port
- Check firewall settings
- Test network connectivity: `telnet pool.sharkpool.io 3333`

### **Performance Tuning**

**For NVIDIA RTX 30/40 series:**
- Batch Size: 2-5M
- Block Size: 256-512
- Memory: ~8-12GB per GPU

**For AMD RX 6000/7000 series:**
- Batch Size: 1-2M
- Block Size: 256
- Memory: ~6-8GB per GPU

### **Getting Help**

- **Documentation**: [docs.sharkpool.io](https://docs.sharkpool.io)
- **Discord**: [discord.gg/sharkpool](https://discord.gg/sharkpool)
- **Issues**: [GitHub Issues](https://github.com/sharkpool/miner/issues)
- **Email**: support@sharkpool.io

---

## 🤝 Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

### **Development Setup**
```bash
# Clone and setup
git clone https://github.com/sharkpool/miner.git
cd miner
cargo install cargo-watch
cargo watch -x "test --features gui"

# Run tests
cargo test --features gpu-cuda
cargo test --features gui

# Lint and format
cargo fmt --all
cargo clippy --features gui
```

### **Architecture Overview**
- `src/gpu/` - GPU mining kernels and device management
- `src/stratum.rs` - Stratum protocol implementation
- `src/miner.rs` - Main mining orchestration
- `src/metrics.rs` - Prometheus metrics
- `src/gui/` - egui-based graphical interface
- `src/bin/` - CLI and GUI binary entry points

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

- **Ironfish Team** - For the Fishhash algorithm
- **egui/eframe** - For the excellent GUI framework
- **Rust Community** - For the amazing ecosystem
- **GPU Compute Community** - For continuous innovation

---

<div align="center">

**[Website](https://sharkpool.io)** • 
**[Discord](https://discord.gg/sharkpool)** • 
**[Twitter](https://twitter.com/sharkpool_io)** • 
**[GitHub](https://github.com/sharkpool/miner)**

Built with ❤️ by the SharkPool Team

</div>
