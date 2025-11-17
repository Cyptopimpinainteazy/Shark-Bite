//! GPU mining implementation
//! 
//! This module provides the GpuMiner which coordinates GPU-based mining
//! operations across CUDA and OpenCL contexts.

use anyhow::Result;
use log::{info, warn, error, debug};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, AtomicBool, Ordering};
use std::time::{Duration, Instant};
use tokio::sync::RwLock;
use tokio::time;
use crossbeam_channel::{bounded, Sender, Receiver};

#[cfg(any(feature = "gpu-cuda", feature = "gpu-opencl"))]
use crate::gpu::kernels::{KernelExecutor, KernelType, CudaKernelExecutor, OpenCLKernelExecutor};
#[cfg(any(feature = "gpu-cuda", feature = "gpu-opencl"))]
use crate::gpu::buffers::GpuBuffers;
use hex;
use crate::stratum::{StratumClient, MiningJob};
use crate::utils::{format_hashrate, hash_meets_target};
use validator_rs::hashing::{fishhash, double_sha256, blake3_hash, argon2id_hash};

/// Share submission message
#[derive(Debug, Clone)]
struct GpuShareSubmission {
    job_id: String,
    nonce: String,
    device_id: String,
}

/// GPU mining statistics
#[derive(Debug, Clone)]
struct GpuMiningStats {
    hashes: AtomicU64,
    shares_found: AtomicU64,
    shares_accepted: AtomicU64,
    shares_rejected: AtomicU64,
    gpu_errors: AtomicU64,
    start_time: Option<Instant>,
    algorithm: String,
    backend: String,
    device_id: usize,
}

impl GpuMiningStats {
    fn new(algorithm: String, backend: String, device_id: usize) -> Self {
        Self {
            hashes: AtomicU64::new(0),
            shares_found: AtomicU64::new(0),
            shares_accepted: AtomicU64::new(0),
            shares_rejected: AtomicU64::new(0),
            gpu_errors: AtomicU64::new(0),
            start_time: Some(Instant::now()),
            algorithm,
            backend,
            device_id,
        }
    }

    fn increment_hashes(&self, count: u64) {
        self.hashes.fetch_add(count, Ordering::Relaxed);
    }

    fn increment_shares_found(&self) {
        self.shares_found.fetch_add(1, Ordering::Relaxed);
    }

    fn increment_shares_accepted(&self) {
        self.shares_accepted.fetch_add(1, Ordering::Relaxed);
    }

    fn increment_shares_rejected(&self) {
        self.shares_rejected.fetch_add(1, Ordering::Relaxed);
    }

    fn increment_gpu_errors(&self) {
        self.gpu_errors.fetch_add(1, Ordering::Relaxed);
    }

    fn get_hashrate(&self) -> f64 {
        if let Some(start) = self.start_time {
            let elapsed = start.elapsed().as_secs_f64();
            if elapsed > 0.0 {
                return self.hashes.load(Ordering::Relaxed) as f64 / elapsed;
            }
        }
        0.0
    }

    fn print_stats(&self) {
        let hashrate = self.get_hashrate();
        let hashes = self.hashes.load(Ordering::Relaxed);
        let found = self.shares_found.load(Ordering::Relaxed);
        let accepted = self.shares_accepted.load(Ordering::Relaxed);
        let rejected = self.shares_rejected.load(Ordering::Relaxed);
        let errors = self.gpu_errors.load(Ordering::Relaxed);

        info!("═══════════════════════════════════════════════════════════");
        info!("  Algorithm:        {}", self.algorithm);
        info!("  GPU Device:       {} (ID: {})", self.backend.to_uppercase(), self.device_id);
        info!("  GPU Hashrate:     {}", format_hashrate(hashrate));
        info!("  Total Hashes:     {}", hashes);
        info!("  Shares Found:     {}", found);
        info!("  Shares Accepted:  {} ({:.1}%)",
            accepted,
            if found > 0 { (accepted as f64 / found as f64) * 100.0 } else { 0.0 }
        );
        info!("  Shares Rejected:  {}", rejected);
        info!("  GPU Errors:       {}", errors);
        info!("═══════════════════════════════════════════════════════════");
    }
}

/// GPU miner implementation
pub struct GpuMiner {
    stratum_client: Arc<RwLock<StratumClient>>,
    algorithm: String,
    backend: String,
    device_id: usize,
    batch_size: u32,
    block_size: u32,
    stats_interval: u64,
    stats: Arc<GpuMiningStats>,
    running: Arc<AtomicBool>,
    kernel_executor: Arc<tokio::sync::Mutex<Box<dyn KernelExecutor>>>,
    buffers: Arc<tokio::sync::Mutex<GpuBuffers>>,
    kernel_type: KernelType,
    last_nonce: AtomicU64,
}

impl GpuMiner {
    /// Create a new GPU miner
    pub fn new(
        stratum_client: Arc<RwLock<StratumClient>>,
        backend: String,
        device_id: usize,
        algorithm: String,
        batch_size: u32,
        block_size: u32,
        stats_interval: u64,
    ) -> Result<Self> {
        // Support both argon2 and argon2id naming
        let norm_algo = match algorithm.as_str() {
            "argon2" | "argon2id" => "argon2id",
            other => other,
        };

        let kernel_type = match norm_algo {
            "fishhash" => KernelType::Fishhash,
            "sha256d" => KernelType::Sha256d,
            "blake3" => KernelType::Blake3,
            "argon2id" => KernelType::Argon2id,
            _ => return Err(anyhow::anyhow!("Unsupported algorithm")),
        };

        let kernel_executor: Box<dyn KernelExecutor> = match backend.as_str() {
            "cuda" => Box::new(CudaKernelExecutor::new(device_id, block_size)?),
            "opencl" => Box::new(OpenCLKernelExecutor::new(device_id, block_size)?),
            _ => return Err(anyhow::anyhow!("Unsupported backend")),
        };

        kernel_executor.load_kernel(kernel_type)?;

        // Compute a VRAM-aware batch size for Argon2id
        let mut adjusted_batch_size = batch_size;
        if norm_algo == "argon2id" {
            // If CUDA, use cudarc to query total memory
            if backend == "cuda" {
                #[cfg(feature = "cuda")]
                {
                    use cudarc::driver::CudaDevice;
                    if let Ok(device) = CudaDevice::new(device_id) {
                        if let Ok(total_mem) = device.total_memory() {
                            let available_vram = (total_mem as f64 * 0.8) as u64; // use 80% for safety
                            let per_hash = 4 * 1024 * 1024u64; // 4MB
                            let mem_batch = (available_vram / per_hash).max(1);
                            let mem_batch_clamped = mem_batch.clamp(1, 1024);
                            adjusted_batch_size = adjusted_batch_size.min(mem_batch_clamped as u32);
                        }
                    }
                }
            } else if backend == "opencl" {
                #[cfg(feature = "gpu-opencl")]
                {
                    use ocl::Device;
                    let platform = ocl::Platform::default();
                    if let Ok(devs) = ocl::Device::list(platform, Some(ocl::flags::DEVICE_TYPE_GPU)) {
                        if let Some(dev) = devs.get(device_id) {
                            if let Ok(total_mem) = dev.global_mem_size() {
                                let available_vram = (total_mem as f64 * 0.8) as u64;
                                let per_hash = 4 * 1024 * 1024u64;
                                let mem_batch = (available_vram / per_hash).max(1);
                                let mem_batch_clamped = mem_batch.clamp(1, 1024);
                                adjusted_batch_size = adjusted_batch_size.min(mem_batch_clamped as u32);
                            }
                        }
                    }
                }
            }
        }

        let backend_enum = match backend.as_str() {
            "cuda" => crate::gpu::kernels::GpuBackend::Cuda,
            "opencl" => crate::gpu::kernels::GpuBackend::OpenCL,
            _ => return Err(anyhow::anyhow!("Unsupported backend for buffers")),
        };
        let buffers = GpuBuffers::new(backend_enum, device_id)?;

        // (already computed above as `adjusted_batch_size`)

        Ok(Self {
            stratum_client,
            algorithm,
            backend,
            device_id,
                batch_size: adjusted_batch_size,
            block_size,
            stats_interval,
            stats: Arc::new(GpuMiningStats::new(algorithm.clone(), backend.clone(), device_id)),
            running: Arc::new(AtomicBool::new(true)),
            kernel_executor: Arc::new(tokio::sync::Mutex::new(kernel_executor)),
            buffers: Arc::new(tokio::sync::Mutex::new(buffers)),
            kernel_type,
            last_nonce: AtomicU64::new(0),
        })
    }

    /// Start GPU mining
    pub async fn start(&self) -> Result<()> {
        info!("Starting GPU miner with algorithm: {}", self.algorithm);

        let (share_tx, share_rx) = bounded::<GpuShareSubmission>(100);

        // Spawn share submission task
        let share_submitter = self.spawn_share_submitter(share_rx);

        // Spawn statistics reporter
        let stats_reporter = self.spawn_stats_reporter();

        // Spawn GPU mining task
        let mining_task = self.spawn_gpu_mining_task(share_tx);

        info!("✓ GPU mining started on {} device {}", self.backend, self.device_id);
        info!("");

        // Wait for Ctrl+C
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                info!("Received shutdown signal...");
                self.running.store(false, Ordering::SeqCst);
            }
        }

        // Wait for all tasks to complete
        let _ = mining_task.await;
        let _ = share_submitter.await;
        let _ = stats_reporter.await;

        info!("GPU mining stopped");
        self.stats.print_stats();

        Ok(())
    }



    /// Spawn GPU mining task
    fn spawn_gpu_mining_task(
        &self,
        share_tx: Sender<GpuShareSubmission>,
    ) -> tokio::task::JoinHandle<()> {
        let stratum_client = self.stratum_client.clone();
        let algorithm = self.algorithm.clone();
        let stats = self.stats.clone();
        let running = self.running.clone();
        let kernel_executor = self.kernel_executor.clone();
        let buffers = self.buffers.clone();
        let batch_size = self.batch_size;
        let backend = self.backend.clone();
        let device_id = self.device_id;
        let last_nonce = self.last_nonce.clone();

        tokio::spawn(async move {
            info!("GPU mining task started");

            while running.load(Ordering::Relaxed) {
                // Get current job
                let job = {
                    let client = stratum_client.read().await;
                    client.get_current_job()
                };

                if let Some(job) = job {
                    if job.clean_jobs {
                        last_nonce.store(0, Ordering::Relaxed);
                    }

                    let target = {
                        let client = stratum_client.read().await;
                        client.get_target()
                    };

                    if let Some(target_str) = target {
                        // Safe snippets for logging to avoid panics when strings are shorter than 16 chars
                        let header_snippet = if job.header_hash.len() > 16 { &job.header_hash[..16] } else { &job.header_hash[..] };
                        let target_snippet = if target_str.len() > 16 { &target_str[..16] } else { &target_str[..] };
                        info!("[FISHHASH-GPU] Processing job: id={}, header={}, target={}, clean={}", job.job_id, header_snippet, target_snippet, job.clean_jobs);
                        // header may be raw ASCII or hex-encoded; prefer hex decode if valid
                        let header = match hex::decode(&job.header_hash) {
                            Ok(h) => h,
                            Err(_) => {
                                // treat as raw ASCII bytes; compute keccak256 if not 32 bytes
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
                        let target_bytes = match hex::decode(&target_str) {
                            Ok(t) => t,
                            Err(e) => {
                                error!("Invalid target hex: {}", e);
                                time::sleep(Duration::from_secs(1)).await;
                                continue;
                            }
                        };
                        let header_arr: [u8; 32] = match header.try_into() {
                            Ok(arr) => arr,
                            Err(_) => {
                                error!("Header not 32 bytes");
                                time::sleep(Duration::from_secs(1)).await;
                                continue;
                            }
                        };
                        let target_arr: [u8; 32] = match target_bytes.try_into() {
                            Ok(arr) => arr,
                            Err(_) => {
                                error!("Target not 32 bytes");
                                time::sleep(Duration::from_secs(1)).await;
                                continue;
                            }
                        };

                        if let Err(e) = { let mut b = buffers.lock().await; b.upload_header(&header_arr) } {
                            error!("Failed to upload header: {}", e);
                            time::sleep(Duration::from_secs(1)).await;
                            continue;
                        }
                        if let Err(e) = { let mut b = buffers.lock().await; b.upload_target(&target_arr) } {
                            error!("Failed to upload target: {}", e);
                            time::sleep(Duration::from_secs(1)).await;
                            continue;
                        }

                        let start_nonce = last_nonce.load(Ordering::Relaxed);

                        // If Argon2id, compute deterministic salt and upload
                        let mut salt_opt: Option<[u8;16]> = None;
                        if algorithm == "argon2id" || algorithm == "argon2" {
                            use sha2::{Digest, Sha256};
                            let mut hasher = Sha256::new();
                            hasher.update(b"SharkPool-Argon2-Salt-v1:");
                            hasher.update(&header_arr);
                            let result = hasher.finalize();
                            let mut salt = [0u8; 16];
                            salt.copy_from_slice(&result[..16]);
                            if let Err(e) = { let mut b = buffers.lock().await; b.upload_salt(&salt) } {
                                error!("Failed to upload salt: {}", e);
                                time::sleep(Duration::from_secs(1)).await;
                                continue;
                            }
                            salt_opt = Some(salt);
                        }

                        let launch_start = Instant::now();
                        let ke = kernel_executor.clone();
                        let mut ke_guard = ke.lock().await;
                        // For Argon2id ensure mem pool is allocated and large enough
                        if algorithm == "argon2id" || algorithm == "argon2" {
                            if let Err(e) = ke_guard.ensure_mem_pool(batch_size) {
                                error!("[FISHHASH-GPU] Failed to prepare Argon2 GPU mem pool for {} on device {}: {}", algorithm, device_id, e);
                                stats.increment_gpu_errors();
                                // Fallback to CPU
                                match Self::fallback_cpu_mining(&algorithm, &job, &target_bytes, start_nonce, batch_size).await {
                                    Ok(cpu_nonces) => {
                                        stats.increment_hashes(batch_size as u64);
                                        stats.increment_shares_found(cpu_nonces.len() as u64);
                                        for nonce in cpu_nonces {
                                            let nonce_str = format!("{:016x}", nonce);
                                            let submission = GpuShareSubmission {
                                                job_id: job.job_id.clone(),
                                                nonce: nonce_str,
                                                device_id: format!("cpu_fallback_{}", start_nonce),
                                            };
                                            if let Err(e) = share_tx.send(submission) {
                                                error!("Failed to send CPU fallback share: {}", e);
                                            }
                                        }
                                    }
                                    Err(cpu_e) => {
                                        error!("CPU fallback also failed: {}", cpu_e);
                                        stats.increment_hashes(batch_size as u64);
                                    }
                                }
                                continue;
                            }
                        }
                        debug!("[FISHHASH-GPU] Launching kernel: batch_size={}, start_nonce={}, device={}_{}", batch_size, start_nonce, backend, device_id);
                        match ke_guard.launch_kernel(header_arr, target_arr, start_nonce, batch_size, salt_opt.as_ref()) {
                            Ok(valid_nonces) => {
                                let launch_time = launch_start.elapsed();
                                last_nonce.store(start_nonce + batch_size as u64, Ordering::Relaxed);

                                stats.increment_hashes(batch_size as u64);
                                stats.increment_shares_found(valid_nonces.len() as u64);
                                debug!("[FISHHASH-GPU] Kernel completed in {:?}, found {} valid nonces", launch_time, valid_nonces.len());

                                for nonce in valid_nonces {
                                    let nonce_str = format!("{:016x}", nonce);
                                    info!("[FISHHASH-GPU] Submitting share: job_id={}, nonce={}, device={}_{}", job.job_id, nonce_str, backend, device_id);
                                    let submission = GpuShareSubmission {
                                        job_id: job.job_id.clone(),
                                        nonce: nonce_str.clone(),
                                        device_id: format!("{}_{}", backend, device_id),
                                    };

                                    if let Err(e) = share_tx.send(submission) {
                                        error!("Failed to send share: {}", e);
                                    }
                                }

                                if launch_time.as_millis() > 250 {
                                    warn!("Kernel launch took {}ms, may indicate bottleneck", launch_time.as_millis());
                                }
                            }
                            Err(e) => {
                                error!("[FISHHASH-GPU] Kernel launch failed for {} on device {}: {}", algorithm, device_id, e);
                                stats.increment_gpu_errors();
                                // Fallback to CPU
                                match Self::fallback_cpu_mining(&algorithm, &job, &target_bytes, start_nonce, batch_size).await {
                                    Ok(cpu_nonces) => {
                                        stats.increment_hashes(batch_size as u64);
                                        stats.increment_shares_found(cpu_nonces.len() as u64);
                                        for nonce in cpu_nonces {
                                            let nonce_str = format!("{:016x}", nonce);
                                            let submission = GpuShareSubmission {
                                                job_id: job.job_id.clone(),
                                                nonce: nonce_str,
                                                device_id: format!("cpu_fallback_{}", start_nonce),
                                            };
                                            if let Err(e) = share_tx.send(submission) {
                                                error!("Failed to send CPU fallback share: {}", e);
                                            }
                                        }
                                    }
                                    Err(cpu_e) => {
                                        error!("CPU fallback also failed: {}", cpu_e);
                                        stats.increment_hashes(batch_size as u64);
                                    }
                                }
                            }
                        }
                    } else {
                        time::sleep(Duration::from_secs(1)).await;
                    }
                } else {
                    time::sleep(Duration::from_secs(1)).await;
                }
            }

            info!("GPU mining task stopped");
        })
    }



    /// Fallback CPU mining when GPU fails
    async fn fallback_cpu_mining(
        algorithm: &str,
        job: &MiningJob,
        target: &[u8],
        start_nonce: u64,
        count: u32,
    ) -> Result<Vec<u64>> {
        debug!("Executing CPU fallback mining for {} nonces starting from {}", count, start_nonce);
        
        let mut results = Vec::new();
        
        for i in 0..count {
            let nonce = start_nonce + i as u64;
            let nonce_str = format!("{:016x}", nonce);
            
            let hash = match algorithm {
                "fishhash" => fishhash(&job.header_hash, &nonce_str),
                "sha256d" => double_sha256(&job.header_hash, &nonce_str),
                "blake3" => blake3_hash(&job.header_hash, &nonce_str),
                "argon2id" => argon2id_hash(&job.header_hash, &nonce_str),
                _ => fishhash(&job.header_hash, &nonce_str),
            };

            let target_hex = hex::encode(target);
            if hash_meets_target(&hash, &target_hex) {
                results.push(nonce);
            }
        }

        info!("✓ CPU fallback mining completed, found {} valid nonces", results.len());
        Ok(results)
    }

    /// Spawn share submission task
    fn spawn_share_submitter(
        &self,
        share_rx: Receiver<GpuShareSubmission>,
    ) -> tokio::task::JoinHandle<()> {
        let stratum_client = self.stratum_client.clone();
        let stats = self.stats.clone();
        let running = self.running.clone();

        tokio::spawn(async move {
            while running.load(Ordering::Relaxed) {
                match share_rx.recv_timeout(Duration::from_millis(100)) {
                    Ok(submission) => {
                        let mut client = stratum_client.write().await;
                        
                        match client.submit_share(&submission.job_id, &submission.nonce).await {
                            Ok(accepted) => {
                                if accepted {
                                    info!("✓ GPU Share ACCEPTED: job={}, nonce={}, device={}", 
                                        submission.job_id, submission.nonce, submission.device_id);
                                    stats.increment_shares_accepted();
                                } else {
                                    warn!("✗ GPU Share REJECTED: job={}, nonce={}, device={}", 
                                        submission.job_id, submission.nonce, submission.device_id);
                                    stats.increment_shares_rejected();
                                }
                            }
                            Err(e) => {
                                error!("Failed to submit GPU share: {}", e);
                                stats.increment_shares_rejected();
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
        let interval = self.stats_interval;

        tokio::spawn(async move {
            let mut ticker = time::interval(Duration::from_secs(interval));

            while running.load(Ordering::Relaxed) {
                ticker.tick().await;
                stats.print_stats();
            }
        })
    }

    /// Get mining statistics
    pub fn get_stats(&self) -> GpuMiningStats {
        GpuMiningStats {
            hashes: AtomicU64::new(self.stats.hashes.load(Ordering::Relaxed)),
            shares_found: AtomicU64::new(self.stats.shares_found.load(Ordering::Relaxed)),
            shares_accepted: AtomicU64::new(self.stats.shares_accepted.load(Ordering::Relaxed)),
            shares_rejected: AtomicU64::new(self.stats.shares_rejected.load(Ordering::Relaxed)),
            gpu_errors: AtomicU64::new(self.stats.gpu_errors.load(Ordering::Relaxed)),
            start_time: self.stats.start_time,
            algorithm: self.stats.algorithm.clone(),
            backend: self.stats.backend.clone(),
            device_id: self.stats.device_id,
        }
    }

    /// Stop the miner
    pub fn stop(&self) {
        self.running.store(false, Ordering::SeqCst);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_gpu_mining_stats() {
        let stats = GpuMiningStats::new("fishhash".to_string(), "cuda".to_string(), 0);

        stats.increment_hashes(1000);
        stats.increment_shares_found();
        stats.increment_shares_accepted();
        stats.increment_gpu_errors();

        assert_eq!(stats.hashes.load(Ordering::Relaxed), 1000);
        assert_eq!(stats.shares_found.load(Ordering::Relaxed), 1);
        assert_eq!(stats.gpu_errors.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn test_share_submission_creation() {
        let submission = GpuShareSubmission {
            job_id: "test_job".to_string(),
            nonce: "12345678".to_string(),
            device_id: "cuda_0".to_string(),
        };
        
        assert_eq!(submission.job_id, "test_job");
        assert_eq!(submission.nonce, "12345678");
        assert_eq!(submission.device_id, "cuda_0");
    }
}
