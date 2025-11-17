//! Multi-GPU mining coordinator
//!
//! This module provides the MultiGpuMiner which coordinates mining across
//! multiple GPU devices with round-robin nonce distribution.

use anyhow::Result;
use log::{info, warn, error};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, AtomicBool, Ordering};
use std::time::{Duration, Instant};
use tokio::sync::{RwLock, Mutex};
use tokio::time;
use crossbeam_channel::{bounded, Sender, Receiver};

#[cfg(any(feature = "gpu-cuda", feature = "gpu-opencl"))]
use crate::gpu::kernels::{KernelExecutor, KernelType, CudaKernelExecutor, OpenCLKernelExecutor, GpuBackend};
#[cfg(any(feature = "gpu-cuda", feature = "gpu-opencl"))]
use crate::gpu::buffers::GpuBuffers;
#[cfg(any(feature = "gpu-cuda", feature = "gpu-opencl"))]
use crate::gpu::detection::GpuDeviceInfo;
use crate::stratum::{StratumClient, MiningJob};
use crate::utils::{format_hashrate, hash_meets_target};

#[cfg(feature = "gpu-cuda")]
use crate::gpu::monitor::GpuMonitorManager;

/// Configuration for multi-GPU mining
#[derive(Debug, Clone)]
pub struct MultiGpuConfig {
    pub algorithm: String,
    pub backend: String,
    pub batch_size: u32,
    pub block_size: u32,
    pub stats_interval: u64,
}

/// Per-GPU worker statistics
#[derive(Debug, Default, Clone)]
pub struct GpuWorkerStats {
    pub device_id: String,
    pub hashes: AtomicU64,
    pub shares_found: AtomicU64,
    pub shares_accepted: AtomicU64,
    pub shares_rejected: AtomicU64,
    pub errors: AtomicU64,
    pub hashrate: f64,
}

impl GpuWorkerStats {
    fn new(device_id: String) -> Self {
        Self {
            device_id,
            hashes: AtomicU64::new(0),
            shares_found: AtomicU64::new(0),
            shares_accepted: AtomicU64::new(0),
            shares_rejected: AtomicU64::new(0),
            errors: AtomicU64::new(0),
            hashrate: 0.0,
        }
    }
}

/// Aggregate statistics across all GPUs
#[derive(Debug, Default)]
pub struct MultiGpuMiningStats {
    pub total_hashrate: f64,
    pub total_hashes: u64,
    pub total_shares_found: u64,
    pub total_shares_accepted: u64,
    pub total_shares_rejected: u64,
    pub total_errors: u64,
    pub per_gpu_stats: Vec<GpuWorkerStats>,
    pub start_time: Instant,
}

impl MultiGpuMiningStats {
    fn new() -> Self {
        Self {
            total_hashrate: 0.0,
            total_hashes: 0,
            total_shares_found: 0,
            total_shares_accepted: 0,
            total_shares_rejected: 0,
            total_errors: 0,
            per_gpu_stats: Vec::new(),
            start_time: Instant::now(),
        }
    }

    fn update_from_workers(&mut self, workers: &[GpuWorker]) {
        self.total_hashrate = 0.0;
        self.total_hashes = 0;
        self.total_shares_found = 0;
        self.total_shares_accepted = 0;
        self.total_shares_rejected = 0;
        self.total_errors = 0;

        for worker in workers {
            let stats = &worker.stats;
            let hashes = stats.hashes.load(Ordering::Relaxed);
            let elapsed = self.start_time.elapsed().as_secs_f64();
            let hashrate = if elapsed > 0.0 { hashes as f64 / elapsed } else { 0.0 };

            self.total_hashrate += hashrate;
            self.total_hashes += hashes;
            self.total_shares_found += stats.shares_found.load(Ordering::Relaxed);
            self.total_shares_accepted += stats.shares_accepted.load(Ordering::Relaxed);
            self.total_shares_rejected += stats.shares_rejected.load(Ordering::Relaxed);
            self.total_errors += stats.errors.load(Ordering::Relaxed);
        }
    }

    fn print_stats(&self) {
        info!("═══════════════════════════════════════════════════════════");
        info!("  Multi-GPU Mining Statistics");
        info!("───────────────────────────────────────────────────────────");
        info!("  Total Hashrate:      {}", format_hashrate(self.total_hashrate));
        info!("  Total Hashes:        {}", self.total_hashes);
        info!("  Shares Found:        {}", self.total_shares_found);
        info!("  Shares Accepted:     {} ({:.1}%)", 
            self.total_shares_accepted,
            if self.total_shares_found > 0 {
                (self.total_shares_accepted as f64 / self.total_shares_found as f64) * 100.0
            } else { 100.0 }
        );
        info!("  Shares Rejected:     {}", self.total_shares_rejected);
        info!("  GPU Errors:          {}", self.total_errors);
        info!("───────────────────────────────────────────────────────────");
        
        for (idx, worker_stats) in self.per_gpu_stats.iter().enumerate() {
            let hashes = worker_stats.hashes.load(Ordering::Relaxed);
            let elapsed = self.start_time.elapsed().as_secs_f64();
            let hashrate = if elapsed > 0.0 { hashes as f64 / elapsed } else { 0.0 };
            let found = worker_stats.shares_found.load(Ordering::Relaxed);
            let accepted = worker_stats.shares_accepted.load(Ordering::Relaxed);
            
            info!("  GPU {}: {} | {} shares ({} accepted)",
                idx,
                format_hashrate(hashrate),
                found,
                accepted
            );
        }
        info!("═══════════════════════════════════════════════════════════");
    }
}

/// Share submission message
#[derive(Debug, Clone)]
struct ShareSubmission {
    job_id: String,
    nonce: String,
    device_id: String,
}

/// Per-GPU worker
pub struct GpuWorker {
    pub device_id: String,
    pub device_index: usize,
    pub kernel_executor: Arc<Mutex<Box<dyn KernelExecutor>>>,
    pub buffers: Arc<Mutex<GpuBuffers>>,
    pub stats: Arc<GpuWorkerStats>,
    pub nonce_offset: AtomicU64,
}

impl GpuWorker {
    fn new(
        device_info: &GpuDeviceInfo,
        backend: &str,
        config: &MultiGpuConfig,
        gpu_index: usize,
    ) -> Result<Self> {
        let device_index = device_info.device.id as usize;
        let device_id = format!("{}_{}", backend, device_index);

        let kernel_type = match config.algorithm.as_str() {
            "fishhash" => KernelType::Fishhash,
            "sha256d" => KernelType::Sha256d,
            "blake3" => KernelType::Blake3,
            "argon2id" | "argon2" => KernelType::Argon2id,
            _ => return Err(anyhow::anyhow!("Unsupported algorithm")),
        };

        let kernel_executor: Box<dyn KernelExecutor> = match backend {
            "cuda" => Box::new(CudaKernelExecutor::new(device_index, config.block_size)?),
            "opencl" => Box::new(OpenCLKernelExecutor::new(device_index, config.block_size)?),
            _ => return Err(anyhow::anyhow!("Unsupported backend")),
        };

        kernel_executor.load_kernel(kernel_type)?;

        let backend_enum = match backend {
            "cuda" => GpuBackend::Cuda,
            "opencl" => GpuBackend::OpenCL,
            _ => return Err(anyhow::anyhow!("Unsupported backend")),
        };

        let buffers = GpuBuffers::new(backend_enum, device_index)?;

        Ok(Self {
            device_id: device_id.clone(),
            device_index,
            kernel_executor: Arc::new(Mutex::new(kernel_executor)),
            buffers: Arc::new(Mutex::new(buffers)),
            stats: Arc::new(GpuWorkerStats::new(device_id)),
            nonce_offset: AtomicU64::new(gpu_index as u64),
        })
    }
}

/// Multi-GPU mining coordinator
pub struct MultiGpuMiner {
    stratum_client: Arc<RwLock<StratumClient>>,
    gpu_workers: Vec<GpuWorker>,
    #[cfg(feature = "gpu-cuda")]
    monitor_manager: Option<GpuMonitorManager>,
    stats: Arc<Mutex<MultiGpuMiningStats>>,
    running: Arc<AtomicBool>,
    config: MultiGpuConfig,
}

impl MultiGpuMiner {
    /// Create a new multi-GPU miner
    pub fn new(
        stratum_client: Arc<RwLock<StratumClient>>,
        devices: Vec<GpuDeviceInfo>,
        config: MultiGpuConfig,
    ) -> Result<Self> {
        info!("Initializing multi-GPU miner with {} devices", devices.len());

        let mut gpu_workers = Vec::new();
        for (idx, device_info) in devices.iter().enumerate() {
            info!("Initializing GPU {}: {}", idx, device_info.get_display_name());
            let worker = GpuWorker::new(device_info, &config.backend, &config, idx)?;
            gpu_workers.push(worker);
        }

        #[cfg(feature = "gpu-cuda")]
        let monitor_manager = if config.backend == "cuda" {
            match GpuMonitorManager::new() {
                Ok(manager) => {
                    info!("GPU monitoring initialized");
                    Some(manager)
                }
                Err(e) => {
                    warn!("Failed to initialize GPU monitoring: {}", e);
                    None
                }
            }
        } else {
            None
        };

        let mut stats = MultiGpuMiningStats::new();
        stats.per_gpu_stats = gpu_workers.iter()
            .map(|w| w.stats.as_ref().clone())
            .collect();

        Ok(Self {
            stratum_client,
            gpu_workers,
            #[cfg(feature = "gpu-cuda")]
            monitor_manager,
            stats: Arc::new(Mutex::new(stats)),
            running: Arc::new(AtomicBool::new(true)),
            config,
        })
    }

    /// Start multi-GPU mining
    pub async fn start(&self) -> Result<()> {
        info!("Starting multi-GPU mining with {} workers", self.gpu_workers.len());

        let (share_tx, share_rx) = bounded::<ShareSubmission>(1000);

        // Start monitoring if available
        #[cfg(feature = "gpu-cuda")]
        let _monitoring_task = self.spawn_monitoring_task();

        // Start share submitter
        let share_submitter = self.spawn_share_submitter(share_rx);

        // Start stats reporter
        let stats_reporter = self.spawn_stats_reporter();

        // Start all GPU workers
        let mut worker_tasks = Vec::new();
        for worker in &self.gpu_workers {
            let task = self.spawn_gpu_worker(worker, share_tx.clone());
            worker_tasks.push(task);
        }

        info!("✓ Multi-GPU mining started with {} workers", self.gpu_workers.len());

        // Wait for shutdown signal
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                info!("Received shutdown signal...");
                self.stop();
            }
        }

        // Wait for all tasks
        for task in worker_tasks {
            let _ = task.await;
        }
        let _ = share_submitter.await;
        let _ = stats_reporter.await;

        info!("Multi-GPU mining stopped");
        let stats = self.stats.lock().await;
        stats.print_stats();

        Ok(())
    }

    /// Spawn a GPU worker task
    fn spawn_gpu_worker(
        &self,
        worker: &GpuWorker,
        share_tx: Sender<ShareSubmission>,
    ) -> tokio::task::JoinHandle<()> {
        let stratum_client = self.stratum_client.clone();
        let algorithm = self.config.algorithm.clone();
        let running = self.running.clone();
        let kernel_executor = worker.kernel_executor.clone();
        let buffers = worker.buffers.clone();
        let stats = worker.stats.clone();
        let device_id = worker.device_id.clone();
        let device_index = worker.device_index;
        let batch_size = self.config.batch_size;
        let num_gpus = self.gpu_workers.len() as u64;
        let gpu_index = worker.nonce_offset.load(Ordering::Relaxed);

        tokio::spawn(async move {
            info!("GPU worker {} started", device_id);
            let mut iteration = 0u64;

            while running.load(Ordering::Relaxed) {
                // Get current job
                let job = {
                    let client = stratum_client.read().await;
                    client.get_current_job()
                };

                if let Some(job) = job {
                    let target = {
                        let client = stratum_client.read().await;
                        client.get_target()
                    };

                    if let Some(target_str) = target {
                        // Round-robin nonce distribution
                        let start_nonce = gpu_index + (iteration * num_gpus * batch_size as u64);

                        if let Err(e) = Self::mine_batch(
                            &kernel_executor,
                            &buffers,
                            &stats,
                            &job,
                            &target_str,
                            &algorithm,
                            start_nonce,
                            batch_size,
                            &device_id,
                            &share_tx,
                        ).await {
                            error!("GPU {} mining error: {}", device_id, e);
                            stats.errors.fetch_add(1, Ordering::Relaxed);
                            crate::metrics::increment_gpu_errors(&device_id);
                        }

                        iteration += 1;
                    } else {
                        time::sleep(Duration::from_millis(100)).await;
                    }
                } else {
                    time::sleep(Duration::from_millis(100)).await;
                }
            }

            info!("GPU worker {} stopped", device_id);
        })
    }

    /// Mine a single batch on a GPU
    async fn mine_batch(
        kernel_executor: &Arc<Mutex<Box<dyn KernelExecutor>>>,
        buffers: &Arc<Mutex<GpuBuffers>>,
        stats: &Arc<GpuWorkerStats>,
        job: &MiningJob,
        target_str: &str,
        algorithm: &str,
        start_nonce: u64,
        batch_size: u32,
        device_id: &str,
        share_tx: &Sender<ShareSubmission>,
    ) -> Result<()> {
        // header may be hex or raw ASCII; prefer hex decode, otherwise compute keccak256
        let header = match hex::decode(&job.header_hash) {
            Ok(h) => h,
            Err(_) => {
                // raw header string: compute keccak256 to obtain 32-byte header hash
                let raw = job.header_hash.as_bytes().to_vec();
                if raw.len() != 32 {
                    use tiny_keccak::{Keccak, Hasher};
                    let mut hasher = Keccak::v256();
                    hasher.update(&raw);
                    let mut out = [0u8; 32];
                    hasher.finalize(&mut out);
                    out.to_vec()
                } else { raw }
            }
        };
        let target_bytes = hex::decode(target_str)?;
        
        let header_arr: [u8; 32] = header.try_into()
            .map_err(|_| anyhow::anyhow!("Header not 32 bytes"))?;
        let target_arr: [u8; 32] = target_bytes.try_into()
            .map_err(|_| anyhow::anyhow!("Target not 32 bytes"))?;

        // Upload header and target
        {
            let mut buf = buffers.lock().await;
            buf.upload_header(&header_arr)?;
            buf.upload_target(&target_arr)?;
        }

        // Handle Argon2id salt
        let mut salt_opt: Option<[u8; 16]> = None;
        if algorithm == "argon2id" || algorithm == "argon2" {
            use sha2::{Digest, Sha256};
            let mut hasher = Sha256::new();
            hasher.update(b"SharkPool-Argon2-Salt-v1:");
            hasher.update(&header_arr);
            let result = hasher.finalize();
            let mut salt = [0u8; 16];
            salt.copy_from_slice(&result[..16]);
            
            let mut buf = buffers.lock().await;
            buf.upload_salt(&salt)?;
            salt_opt = Some(salt);
        }

        // Launch kernel
        let mut ke = kernel_executor.lock().await;
        let valid_nonces = ke.launch_kernel(header_arr, target_arr, start_nonce, batch_size, salt_opt.as_ref())?;

        stats.hashes.fetch_add(batch_size as u64, Ordering::Relaxed);
        stats.shares_found.fetch_add(valid_nonces.len() as u64, Ordering::Relaxed);

        // Submit shares
        for nonce in valid_nonces {
            let nonce_str = format!("{:016x}", nonce);
            let submission = ShareSubmission {
                job_id: job.job_id.clone(),
                nonce: nonce_str,
                device_id: device_id.to_string(),
            };

            if let Err(e) = share_tx.send(submission) {
                error!("Failed to send share from {}: {}", device_id, e);
            }
        }

        Ok(())
    }

    /// Spawn share submission task
    fn spawn_share_submitter(
        &self,
        share_rx: Receiver<ShareSubmission>,
    ) -> tokio::task::JoinHandle<()> {
        let stratum_client = self.stratum_client.clone();
        let running = self.running.clone();
        let workers = self.gpu_workers.iter()
            .map(|w| (w.device_id.clone(), w.stats.clone()))
            .collect::<Vec<_>>();

        tokio::spawn(async move {
            while running.load(Ordering::Relaxed) {
                match share_rx.recv_timeout(Duration::from_millis(100)) {
                    Ok(submission) => {
                        let mut client = stratum_client.write().await;
                        
                        match client.submit_share(&submission.job_id, &submission.nonce).await {
                            Ok(accepted) => {
                                if accepted {
                                    info!("✓ Share ACCEPTED: device={}, nonce={}", 
                                        submission.device_id, submission.nonce);
                                    
                                    // Update stats for this device
                                    if let Some((_, stats)) = workers.iter().find(|(id, _)| id == &submission.device_id) {
                                        stats.shares_accepted.fetch_add(1, Ordering::Relaxed);
                                    }
                                } else {
                                    warn!("✗ Share REJECTED: device={}, nonce={}", 
                                        submission.device_id, submission.nonce);
                                    
                                    if let Some((_, stats)) = workers.iter().find(|(id, _)| id == &submission.device_id) {
                                        stats.shares_rejected.fetch_add(1, Ordering::Relaxed);
                                    }
                                }
                            }
                            Err(e) => {
                                error!("Failed to submit share from {}: {}", submission.device_id, e);
                                if let Some((_, stats)) = workers.iter().find(|(id, _)| id == &submission.device_id) {
                                    stats.shares_rejected.fetch_add(1, Ordering::Relaxed);
                                }
                            }
                        }
                    }
                    Err(_) => {
                        // Timeout, continue
                    }
                }
            }
        })
    }

    /// Spawn statistics reporter
    fn spawn_stats_reporter(&self) -> tokio::task::JoinHandle<()> {
        let stats = self.stats.clone();
        let running = self.running.clone();
        let interval = self.config.stats_interval;
        let workers = self.gpu_workers.iter().map(|w| w.clone_for_stats()).collect::<Vec<_>>();

        tokio::spawn(async move {
            let mut ticker = time::interval(Duration::from_secs(interval));

            while running.load(Ordering::Relaxed) {
                ticker.tick().await;
                
                let mut stats_guard = stats.lock().await;
                stats_guard.update_from_workers(&workers);
                stats_guard.print_stats();

                // Update Prometheus metrics
                crate::metrics::update_mining_stats(
                    stats_guard.total_hashrate,
                    stats_guard.total_shares_found,
                    stats_guard.total_shares_accepted,
                    stats_guard.total_shares_rejected,
                );

                for worker in &workers {
                    let hashes = worker.stats.hashes.load(Ordering::Relaxed);
                    let elapsed = stats_guard.start_time.elapsed().as_secs_f64();
                    let hashrate = if elapsed > 0.0 { hashes as f64 / elapsed } else { 0.0 };
                    crate::metrics::update_gpu_hashrate(&worker.device_id, hashrate);
                }
            }
        })
    }

    /// Spawn monitoring task (CUDA only)
    #[cfg(feature = "gpu-cuda")]
    fn spawn_monitoring_task(&self) -> tokio::task::JoinHandle<()> {
        let monitor = self.monitor_manager.clone();
        let running = self.running.clone();

        tokio::spawn(async move {
            if let Some(mut manager) = monitor {
                let callback = |device_id: &str, stats: &crate::gpu::monitor::GpuMonitorStats| {
                    crate::metrics::update_gpu_metrics(device_id, stats);
                };

                if let Err(e) = manager.start_monitoring(callback) {
                    error!("Failed to start GPU monitoring: {}", e);
                }

                while running.load(Ordering::Relaxed) {
                    time::sleep(Duration::from_secs(5)).await;
                }

                manager.stop_monitoring();
            }
        })
    }

    /// Stop the miner
    pub fn stop(&self) {
        self.running.store(false, Ordering::SeqCst);
    }

    /// Get aggregate statistics
    pub async fn get_stats(&self) -> MultiGpuMiningStats {
        let stats = self.stats.lock().await;
        MultiGpuMiningStats {
            total_hashrate: stats.total_hashrate,
            total_hashes: stats.total_hashes,
            total_shares_found: stats.total_shares_found,
            total_shares_accepted: stats.total_shares_accepted,
            total_shares_rejected: stats.total_shares_rejected,
            total_errors: stats.total_errors,
            per_gpu_stats: stats.per_gpu_stats.clone(),
            start_time: stats.start_time,
        }
    }

    /// Get per-GPU statistics
    pub fn get_per_gpu_stats(&self) -> Vec<(String, GpuWorkerStats)> {
        self.gpu_workers.iter()
            .map(|w| (w.device_id.clone(), w.stats.as_ref().clone()))
            .collect()
    }
}

impl GpuWorker {
    fn clone_for_stats(&self) -> Self {
        Self {
            device_id: self.device_id.clone(),
            device_index: self.device_index,
            kernel_executor: self.kernel_executor.clone(),
            buffers: self.buffers.clone(),
            stats: self.stats.clone(),
            nonce_offset: AtomicU64::new(self.nonce_offset.load(Ordering::Relaxed)),
        }
    }
}
