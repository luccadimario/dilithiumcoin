// GPU worker for mining

use crate::cuda;
use anyhow::Result;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;

pub struct GpuWorker {
    batch_size: u64,
    hash_count: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
}

pub struct MiningResult {
    pub nonce: u64,
    pub hash: [u8; 32],
}

impl GpuWorker {
    pub fn new(
        batch_size: u64,
        hash_count: Arc<AtomicU64>,
        stop_flag: Arc<AtomicBool>,
    ) -> Self {
        Self {
            batch_size,
            hash_count,
            stop_flag,
        }
    }

    pub fn mine(
        &self,
        midstate: &[u32; 8],
        tail: &[u8],
        total_prefix_len: u64,
        suffix: &[u8],
        diff_bits: i32,
    ) -> Result<Option<MiningResult>> {
        let mut nonce_counter: u64 = 0;

        loop {
            if self.stop_flag.load(Ordering::Relaxed) {
                return Ok(None);
            }

            let start_nonce = nonce_counter;
            nonce_counter += self.batch_size;

            // Mine batch using pre-initialized GPU
            match cuda::mine_batch(
                midstate,
                tail,
                total_prefix_len,
                suffix,
                diff_bits,
                start_nonce,
                self.batch_size,
            )? {
                Some((nonce, hash)) => {
                    // Found solution!
                    self.hash_count.fetch_add(self.batch_size, Ordering::Relaxed);
                    return Ok(Some(MiningResult { nonce, hash }));
                }
                None => {
                    // No solution in this batch, continue
                    self.hash_count.fetch_add(self.batch_size, Ordering::Relaxed);
                }
            }
        }
    }
}
