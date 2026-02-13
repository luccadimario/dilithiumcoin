// Mining coordinator

use crate::network::{Block, BlockTemplate, NodeClient, Transaction};
use crate::sha256::sha256_midstate;
use crate::webui::MinerStats;
use crate::worker::GpuWorker;
use anyhow::{Context, Result};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::time::sleep;

#[derive(Clone)]
pub struct Miner {
    node_url: String,
    wallet_address: String,
    device_id: i32,
    batch_size: u64,

    // Stats
    blocks_mined: Arc<AtomicU64>,
    total_hashes: Arc<AtomicU64>,
    total_earnings: Arc<AtomicU64>,
    current_hashrate: Arc<AtomicU64>, // Real-time hashrate in H/s
    stop_flag: Arc<AtomicBool>,
}

impl Miner {
    pub fn new(node_url: String, wallet_address: String, device_id: i32, batch_size: u64) -> Self {
        Self {
            node_url,
            wallet_address,
            device_id,
            batch_size,
            blocks_mined: Arc::new(AtomicU64::new(0)),
            total_hashes: Arc::new(AtomicU64::new(0)),
            total_earnings: Arc::new(AtomicU64::new(0)),
            current_hashrate: Arc::new(AtomicU64::new(0)),
            stop_flag: Arc::new(AtomicBool::new(false)),
        }
    }

    pub fn stop(&self) {
        self.stop_flag.store(true, Ordering::Relaxed);
    }

    pub fn get_stats(&self, start_time: Instant) -> MinerStats {
        MinerStats {
            blocks_mined: self.blocks_mined.clone(),
            total_hashes: self.total_hashes.clone(),
            total_earnings: self.total_earnings.clone(),
            current_hashrate: self.current_hashrate.clone(),
            start_time,
            wallet_address: self.wallet_address.clone(),
            node_url: self.node_url.clone(),
            device_id: self.device_id,
            batch_size: self.batch_size,
        }
    }

    pub async fn run(&self) -> Result<()> {
        let client = NodeClient::new(self.node_url.clone());

        // Check connection
        client
            .check_connection()
            .await
            .context("Failed to connect to node")?;

        log::info!("[*] Connected to node at {}", self.node_url);
        log::info!("[*] Mining to wallet: {}", self.wallet_address);

        // Wait for sync
        self.wait_for_sync(&client).await?;

        // Start stats reporter
        let stats_handle = {
            let blocks = self.blocks_mined.clone();
            let hashes = self.total_hashes.clone();
            let earnings = self.total_earnings.clone();
            let stop = self.stop_flag.clone();

            tokio::spawn(async move {
                let start_time = Instant::now();

                while !stop.load(Ordering::Relaxed) {
                    sleep(Duration::from_secs(30)).await;

                    let elapsed = start_time.elapsed().as_secs();
                    let total_h = hashes.load(Ordering::Relaxed);
                    let blocks_count = blocks.load(Ordering::Relaxed);
                    let earn = earnings.load(Ordering::Relaxed);

                    let avg_hashrate = if elapsed > 0 {
                        total_h as f64 / elapsed as f64 / 1e6
                    } else {
                        0.0
                    };

                    log::info!(
                        "[i] Session: {}s | Hashes: {} | Avg: {:.2} MH/s | Blocks: {} | Earnings: {} DLT",
                        elapsed,
                        total_h,
                        avg_hashrate,
                        blocks_count,
                        earn as f64 / 1e8
                    );
                }
            })
        };

        // Main mining loop
        loop {
            if self.stop_flag.load(Ordering::Relaxed) {
                break;
            }

            // Get work
            let template = match client.get_work().await {
                Ok(t) => t,
                Err(e) => {
                    log::error!("[!] Error getting work: {}", e);
                    sleep(Duration::from_secs(5)).await;
                    continue;
                }
            };

            // Get pending transactions
            let pending_txs = client.get_pending_transactions().await.unwrap_or_default();

            if !pending_txs.is_empty() {
                log::info!("[*] Including {} pending transaction(s)", pending_txs.len());
            }

            // Build block
            let block = self.construct_block(&template, pending_txs);

            log::info!(
                "[*] Mining block #{} | difficulty: {} bits ({} hex) | GPU device {}",
                template.index,
                template.difficulty_bits,
                template.difficulty,
                self.device_id
            );

            // Mine block
            match self.mine_block(&block, &template).await {
                Ok(Some((nonce, hash))) => {
                    // Verify block is still valid (chain might have advanced)
                    let current_height = client.get_current_height().await.unwrap_or(template.index);
                    if current_height != template.index {
                        log::info!("[~] Block stale (height {} -> {}), restarting...", template.index, current_height);
                        continue;
                    }

                    // Set block fields
                    let mut mined_block = block.clone();
                    mined_block.nonce = nonce as i64;
                    mined_block.hash = hex::encode(hash);

                    log::info!("[*] Submitting block #{} (height {}, prev: {}...)",
                        mined_block.index, template.height, &template.previous_hash[..16]);

                    // Submit block
                    match client.submit_block(&mined_block).await {
                        Ok(_) => {
                            self.blocks_mined.fetch_add(1, Ordering::Relaxed);
                            self.total_earnings
                                .fetch_add(template.reward as u64, Ordering::Relaxed);

                            log::info!(
                                "[+] BLOCK #{} MINED! Hash: {}... | Reward: {} DLT | Total: {} blocks, {} DLT",
                                mined_block.index,
                                &mined_block.hash[..16],
                                template.reward as f64 / 1e8,
                                self.blocks_mined.load(Ordering::Relaxed),
                                self.total_earnings.load(Ordering::Relaxed) as f64 / 1e8
                            );
                        }
                        Err(e) => {
                            log::error!("[!] Block rejected: {}", e);
                            // Block rejected, restart with fresh template
                            continue;
                        }
                    }
                }
                Ok(None) => {
                    // Stale work, restart
                    continue;
                }
                Err(e) => {
                    log::error!("[!] Mining error: {}", e);
                    sleep(Duration::from_secs(1)).await;
                }
            }
        }

        stats_handle.abort();
        Ok(())
    }

    async fn wait_for_sync(&self, client: &NodeClient) -> Result<()> {
        log::info!("[*] Checking node sync status...");

        // Just wait for height > 0, don't require stability
        loop {
            if self.stop_flag.load(Ordering::Relaxed) {
                return Ok(());
            }

            let height = match client.get_current_height().await {
                Ok(h) => h,
                Err(e) => {
                    log::error!("[!] Error getting height: {}", e);
                    0
                }
            };

            if height > 0 {
                log::info!("[+] Node ready at height {}", height);
                return Ok(());
            }

            log::info!("[~] Waiting for blockchain data...");
            sleep(Duration::from_secs(2)).await;
        }
    }

    fn construct_block(&self, template: &BlockTemplate, pending_txs: Vec<Transaction>) -> Block {
        let coinbase = Transaction {
            from: "SYSTEM".to_string(),
            to: self.wallet_address.clone(),
            amount: template.reward,
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64,
            signature: format!(
                "coinbase-{}-{}",
                template.index,
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ),
        };

        let mut txs = vec![coinbase];
        txs.extend(pending_txs);

        Block {
            index: template.index,
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64,
            transactions: txs,
            previous_hash: template.previous_hash.clone(),
            hash: String::new(),
            nonce: 0,
            difficulty: template.difficulty,
            difficulty_bits: Some(template.difficulty_bits),
        }
    }

    async fn mine_block(
        &self,
        block: &Block,
        template: &BlockTemplate,
    ) -> Result<Option<(u64, [u8; 32])>> {
        // Build hash input
        let (prefix, suffix) = self.build_hash_input(block);

        // Compute midstate
        let midstate = sha256_midstate(&prefix);
        let full_blocks_len = (prefix.len() / 64) * 64;
        let tail = &prefix[full_blocks_len..];

        log::info!(
            "[*] Prefix: {} bytes | Midstate: {} blocks | Tail: {} bytes",
            prefix.len(),
            full_blocks_len / 64,
            tail.len()
        );

        // Create worker
        let hash_count = Arc::new(AtomicU64::new(0));
        let worker_stop = Arc::new(AtomicBool::new(false));

        let worker = GpuWorker::new(
            self.device_id,
            self.batch_size,
            hash_count.clone(),
            worker_stop.clone(),
        )?;

        // Start mining in background
        let midstate_copy = midstate;
        let tail_copy = tail.to_vec();
        let suffix_copy = suffix.clone();
        let prefix_len = prefix.len() as u64;
        let diff_bits = template.difficulty_bits;

        let mut mining_handle = tokio::task::spawn_blocking(move || {
            worker.mine(
                &midstate_copy,
                &tail_copy,
                prefix_len,
                &suffix_copy,
                diff_bits,
            )
        });

        // Monitor for stale work
        let start_time = Instant::now();
        let node_client = NodeClient::new(self.node_url.clone());

        loop {
            tokio::select! {
                result = &mut mining_handle => {
                    match result {
                        Ok(Ok(Some(solution))) => {
                            let elapsed = start_time.elapsed().as_secs_f64();
                            let hashes = hash_count.load(Ordering::Relaxed);
                            self.total_hashes.fetch_add(hashes, Ordering::Relaxed);

                            let hashrate = if elapsed > 0.0 {
                                hashes as f64 / elapsed / 1e6
                            } else {
                                0.0
                            };

                            log::info!("[+] Found hash after {} hashes ({:.2} MH/s)", hashes, hashrate);
                            return Ok(Some((solution.nonce, solution.hash)));
                        }
                        Ok(Ok(None)) => {
                            // Stopped
                            return Ok(None);
                        }
                        Ok(Err(e)) => {
                            return Err(e);
                        }
                        Err(e) => {
                            anyhow::bail!("Worker panic: {}", e);
                        }
                    }
                }

                _ = sleep(Duration::from_secs(3)) => {
                    // Check if chain advanced past the block we're mining
                    if let Ok(current_height) = node_client.get_current_height().await {
                        if current_height > template.index {
                            log::info!("[~] Chain advanced to {}, restarting...", current_height);
                            worker_stop.store(true, Ordering::Relaxed);
                            self.total_hashes.fetch_add(hash_count.load(Ordering::Relaxed), Ordering::Relaxed);
                            return Ok(None);
                        }
                    }

                    // Print progress
                    let hashes = hash_count.load(Ordering::Relaxed);
                    let elapsed = start_time.elapsed().as_secs_f64();
                    if elapsed > 0.0 {
                        let hashrate = hashes as f64 / elapsed;
                        // Update current hashrate for webui
                        self.current_hashrate.store(hashrate as u64, Ordering::Relaxed);
                        log::info!("[~] Hashrate: {:.2} MH/s | Hashes: {}", hashrate / 1e6, hashes);
                    }
                }
            }
        }
    }

    fn build_hash_input(&self, block: &Block) -> (Vec<u8>, Vec<u8>) {
        let tx_json = serde_json::to_string(&block.transactions).unwrap();

        let prefix = format!(
            "{}{}{}{}",
            block.index, block.timestamp, tx_json, block.previous_hash
        );

        let suffix = format!("{}", block.difficulty);

        (prefix.into_bytes(), suffix.into_bytes())
    }
}
