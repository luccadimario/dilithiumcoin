// GPU worker for mining

use crate::backend::{GpuBackend, MiningResult};
use anyhow::Result;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;

pub struct GpuWorker {
    batch_size: u64,
    max_nonce: u64,
    hash_count: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
}

impl GpuWorker {
    pub fn new(
        batch_size: u64,
        max_nonce: u64,
        hash_count: Arc<AtomicU64>,
        stop_flag: Arc<AtomicBool>,
    ) -> Self {
        Self {
            batch_size,
            max_nonce,
            hash_count,
            stop_flag,
        }
    }

    pub fn mine(
        &self,
        backend: &dyn GpuBackend,
        midstate: &[u32; 8],
        tail: &[u8],
        total_prefix_len: u64,
        suffix: &[u8],
        diff_bits: i32,
    ) -> Result<Option<MiningResult>> {
        // Start at a nonce that has the same digit count as max_nonce to avoid
        // expensive carry overflow in the GPU incremental nonce computation.
        // E.g., for max_nonce=9999999999, start at 1000000000 (all 10-digit nonces).
        let start_offset = if self.max_nonce >= 10 {
            let digits = (self.max_nonce as f64).log10() as u32;
            10u64.pow(digits)
        } else {
            0
        };
        let mut nonce_counter: u64 = start_offset;

        loop {
            if self.stop_flag.load(Ordering::Relaxed) {
                return Ok(None);
            }

            // Check if nonce space is exhausted (triggers block rotation)
            let remaining = self.max_nonce.saturating_sub(nonce_counter);
            if remaining == 0 {
                return Ok(None);
            }

            let start_nonce = nonce_counter;
            let effective_batch = self.batch_size.min(remaining);
            nonce_counter += effective_batch;

            // Mine batch using the active GPU backend
            match backend.mine_batch(
                midstate,
                tail,
                total_prefix_len,
                suffix,
                diff_bits,
                start_nonce,
                effective_batch,
            )? {
                Some((nonce, hash)) => {
                    self.hash_count.fetch_add(effective_batch, Ordering::Relaxed);
                    return Ok(Some(MiningResult { nonce, hash }));
                }
                None => {
                    self.hash_count.fetch_add(effective_batch, Ordering::Relaxed);
                }
            }
        }
    }
}
