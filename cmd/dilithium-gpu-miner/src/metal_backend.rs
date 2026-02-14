// Metal GPU backend for macOS (Apple Silicon / AMD)
#![cfg(feature = "metal")]

use crate::backend::GpuBackend;
use anyhow::{Context, Result};
use metal::*;
use std::mem;

// MiningParams must match the Metal shader struct exactly.
// See metal/sha256_mining.metal for the layout.
#[repr(C)]
#[derive(Clone, Copy)]
struct MiningParams {
    midstate: [u32; 8],        // offset 0   (32 bytes)
    tail_w: [u32; 16],         // offset 32  (64 bytes) — precomputed BE words from tail
    tail_len: u32,             // offset 96  (4 bytes)  — byte count for nonce positioning
    base_nonce_len: u32,       // offset 100 (4 bytes)  — length of base_nonce_str
    total_prefix_len: u64,     // offset 104 (8 bytes)
    suffix: [u8; 8],           // offset 112 (8 bytes)
    suffix_len: u32,           // offset 120 (4 bytes)
    _pad1: u32,                // offset 124 (4 bytes)
    nonce_start: u64,          // offset 128 (8 bytes)
    difficulty_bits: u32,      // offset 136 (4 bytes)
    num_precomputed_rounds: u32, // offset 140 (4 bytes) — rounds precomputed on CPU
    batch_size: u64,           // offset 144 (8 bytes)
    base_nonce_str: [u8; 20],  // offset 152 (20 bytes) — nonce_start as decimal string
    _pad3: u32,                // offset 172 (4 bytes)
    partial_state: [u32; 8],   // offset 176 (32 bytes) — state after precomputed rounds
    precomputed_w: [u32; 12],  // offset 208 (48 bytes) — precomputed SCHED values for rounds 16-24
}                              // Total: 256 bytes

const _ASSERT_SIZE: () = assert!(mem::size_of::<MiningParams>() == 256);

// SHA-256 constants (first 12 used for CPU precomputation)
const SHA256_K: [u32; 12] = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
    0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
];

#[inline(always)]
fn rotr32(x: u32, n: u32) -> u32 { x.rotate_right(n) }

#[inline(always)]
fn sha256_sigma0(x: u32) -> u32 { rotr32(x, 2) ^ rotr32(x, 13) ^ rotr32(x, 22) }

#[inline(always)]
fn sha256_sigma1(x: u32) -> u32 { rotr32(x, 6) ^ rotr32(x, 11) ^ rotr32(x, 25) }

// Message schedule sigma functions (lowercase — for SCHED expansion)
#[inline(always)]
fn msg_sigma0(x: u32) -> u32 { rotr32(x, 7) ^ rotr32(x, 18) ^ (x >> 3) }

#[inline(always)]
fn msg_sigma1(x: u32) -> u32 { rotr32(x, 17) ^ rotr32(x, 19) ^ (x >> 10) }

#[inline(always)]
fn sha256_ch(x: u32, y: u32, z: u32) -> u32 { (x & y) ^ (!x & z) }

#[inline(always)]
fn sha256_maj(x: u32, y: u32, z: u32) -> u32 { (x & y) ^ (x & z) ^ (y & z) }

/// Pre-allocated buffer set for one in-flight batch
struct BufferSet {
    params_buf: Buffer,
    result_found_buf: Buffer,
    result_nonce_buf: Buffer,
    result_hash_buf: Buffer,
}

impl BufferSet {
    fn new(device: &Device) -> Self {
        Self {
            params_buf: device.new_buffer(
                mem::size_of::<MiningParams>() as u64,
                MTLResourceOptions::StorageModeShared,
            ),
            result_found_buf: device.new_buffer(
                mem::size_of::<u32>() as u64,
                MTLResourceOptions::StorageModeShared,
            ),
            result_nonce_buf: device.new_buffer(
                mem::size_of::<u64>() as u64,
                MTLResourceOptions::StorageModeShared,
            ),
            result_hash_buf: device.new_buffer(
                (mem::size_of::<u32>() * 8) as u64,
                MTLResourceOptions::StorageModeShared,
            ),
        }
    }

    fn reset_and_write_params(&self, params: &MiningParams) {
        unsafe {
            std::ptr::copy_nonoverlapping(
                params as *const MiningParams,
                self.params_buf.contents() as *mut MiningParams,
                1,
            );
            *(self.result_found_buf.contents() as *mut u32) = 0;
            *(self.result_nonce_buf.contents() as *mut u64) = 0;
            std::ptr::write_bytes(self.result_hash_buf.contents() as *mut u8, 0, 32);
        }
    }

    fn read_result(&self) -> Option<(u64, [u8; 32])> {
        let found = unsafe { *(self.result_found_buf.contents() as *const u32) };
        if found == 0 {
            return None;
        }

        let nonce = unsafe { *(self.result_nonce_buf.contents() as *const u64) };
        let hash_words = unsafe {
            std::slice::from_raw_parts(self.result_hash_buf.contents() as *const u32, 8)
        };

        let mut hash = [0u8; 32];
        for i in 0..8 {
            hash[i * 4] = (hash_words[i] >> 24) as u8;
            hash[i * 4 + 1] = (hash_words[i] >> 16) as u8;
            hash[i * 4 + 2] = (hash_words[i] >> 8) as u8;
            hash[i * 4 + 3] = hash_words[i] as u8;
        }

        Some((nonce, hash))
    }
}

pub struct MetalBackend {
    _device: Device,
    command_queue: CommandQueue,
    pipeline: ComputePipelineState,
    max_threads_per_threadgroup: u64,
    buffers: [BufferSet; 2],
}

impl MetalBackend {
    pub fn new(_device_id: i32) -> Result<Self> {
        let device = Device::system_default()
            .context("No Metal-capable GPU found")?;

        log::info!("[Metal] Using device: {}", device.name());

        let command_queue = device.new_command_queue();

        let shader_source = include_str!("../metal/sha256_mining.metal");
        let options = CompileOptions::new();
        let library = device
            .new_library_with_source(shader_source, &options)
            .map_err(|e| anyhow::anyhow!("Metal shader compilation failed: {}", e))?;

        let kernel_fn = library
            .get_function("mine_kernel", None)
            .map_err(|e| anyhow::anyhow!("Failed to get mine_kernel function: {}", e))?;

        let pipeline = device
            .new_compute_pipeline_state_with_function(&kernel_fn)
            .map_err(|e| anyhow::anyhow!("Failed to create compute pipeline: {}", e))?;

        let max_threads_per_threadgroup = pipeline.max_total_threads_per_threadgroup();

        let buffers = [BufferSet::new(&device), BufferSet::new(&device)];

        log::info!(
            "[Metal] Pipeline created, max threads/threadgroup: {}, double-buffered",
            max_threads_per_threadgroup
        );

        Ok(Self {
            _device: device,
            command_queue,
            pipeline,
            max_threads_per_threadgroup,
            buffers,
        })
    }

    fn encode_and_submit(&self, buf: &BufferSet, batch_size: u64) -> &CommandBufferRef {
        let command_buffer = self.command_queue.new_command_buffer();
        let encoder = command_buffer.new_compute_command_encoder();

        encoder.set_compute_pipeline_state(&self.pipeline);
        encoder.set_buffer(0, Some(&buf.params_buf), 0);
        encoder.set_buffer(1, Some(&buf.result_found_buf), 0);
        encoder.set_buffer(2, Some(&buf.result_nonce_buf), 0);
        encoder.set_buffer(3, Some(&buf.result_hash_buf), 0);

        let grid_size = MTLSize::new(batch_size, 1, 1);
        let threadgroup_size = MTLSize::new(self.max_threads_per_threadgroup, 1, 1);

        encoder.dispatch_threads(grid_size, threadgroup_size);
        encoder.end_encoding();

        command_buffer.commit();
        command_buffer
    }

    /// Precompute SHA-256 rounds 0..num_rounds on CPU using static W[] words.
    /// Returns the intermediate state (a,b,c,d,e,f,g,h) after those rounds.
    fn precompute_partial_state(midstate: &[u32; 8], tail_w: &[u32; 16], num_rounds: usize) -> [u32; 8] {
        let (mut a, mut b, mut c, mut d) = (midstate[0], midstate[1], midstate[2], midstate[3]);
        let (mut e, mut f, mut g, mut h) = (midstate[4], midstate[5], midstate[6], midstate[7]);

        for i in 0..num_rounds {
            let t1 = h.wrapping_add(sha256_sigma1(e))
                .wrapping_add(sha256_ch(e, f, g))
                .wrapping_add(SHA256_K[i])
                .wrapping_add(tail_w[i]);
            let t2 = sha256_sigma0(a).wrapping_add(sha256_maj(a, b, c));
            h = g; g = f; f = e; e = d.wrapping_add(t1);
            d = c; c = b; b = a; a = t1.wrapping_add(t2);
        }

        [a, b, c, d, e, f, g, h]
    }

    /// Precompute tail bytes into big-endian uint32 words for direct W[] loading on GPU
    fn precompute_tail_words(tail: &[u8]) -> [u32; 16] {
        let mut tail_w = [0u32; 16];
        let full_words = tail.len() / 4;
        for i in 0..full_words {
            tail_w[i] = u32::from_be_bytes([
                tail[i * 4],
                tail[i * 4 + 1],
                tail[i * 4 + 2],
                tail[i * 4 + 3],
            ]);
        }
        // Handle remaining bytes (if tail_len not multiple of 4)
        let rem = tail.len() % 4;
        if rem > 0 {
            let word_idx = full_words;
            let mut bytes = [0u8; 4];
            bytes[..rem].copy_from_slice(&tail[word_idx * 4..]);
            tail_w[word_idx] = u32::from_be_bytes(bytes);
        }
        tail_w
    }
}

impl GpuBackend for MetalBackend {
    fn name(&self) -> &str {
        "Metal"
    }

    fn compute_unit_count(&self) -> i32 {
        self.max_threads_per_threadgroup as i32
    }

    fn mine_batch(
        &self,
        midstate: &[u32; 8],
        tail: &[u8],
        total_prefix_len: u64,
        suffix: &[u8],
        diff_bits: i32,
        start_nonce: u64,
        batch_size: u64,
    ) -> Result<Option<(u64, [u8; 32])>> {
        if tail.len() > 64 {
            anyhow::bail!("tail too long: {} > 64", tail.len());
        }
        if suffix.len() > 8 {
            anyhow::bail!("suffix too long: {} > 8", suffix.len());
        }

        let mut tail_w = Self::precompute_tail_words(tail);

        // Precompute SHA-256 rounds for static tail words on CPU
        // Number of fully static words = tail_len / 4 (nonce starts at byte tail_len)
        let num_static_words = tail.len() / 4;
        let num_precomputed_rounds = num_static_words.min(11); // max 11 rounds with our K[] table
        let partial_state = if num_precomputed_rounds > 0 {
            Self::precompute_partial_state(midstate, &tail_w, num_precomputed_rounds)
        } else {
            *midstate
        };

        // Precompute base nonce as decimal string on CPU
        let base_nonce_string = format!("{}", start_nonce);

        // Precompute SHA-256 bit length into tail_w[14]/[15] (saves GPU computation)
        let total_msg_len = total_prefix_len + base_nonce_string.len() as u64 + suffix.len() as u64;
        let total_bit_len = total_msg_len * 8;
        tail_w[14] = (total_bit_len >> 32) as u32;
        tail_w[15] = total_bit_len as u32;
        let base_nonce_bytes = base_nonce_string.as_bytes();
        let mut base_nonce_str = [0u8; 20];
        base_nonce_str[..base_nonce_bytes.len()].copy_from_slice(base_nonce_bytes);

        // Precompute message schedule for rounds 16-19 on CPU
        // SCHED(16): W[0] += sigma1(W[14]) + W[9] + sigma0(W[1])  — fully constant
        // SCHED(17): W[1] += sigma1(W[15]) + W[10] + sigma0(W[2]) — fully constant
        // SCHED(18): W[2] += sigma1(W16) + W[11] + sigma0(W[3])   — W[11] is nonce-dependent
        // SCHED(19): W[3] += sigma1(W17) + W[12] + sigma0(W[4])   — W[12] is nonce-dependent
        let w16 = tail_w[0]
            .wrapping_add(msg_sigma1(tail_w[14]))
            .wrapping_add(tail_w[9])
            .wrapping_add(msg_sigma0(tail_w[1]));
        let w17 = tail_w[1]
            .wrapping_add(msg_sigma1(tail_w[15]))
            .wrapping_add(tail_w[10])
            .wrapping_add(msg_sigma0(tail_w[2]));
        // For rounds 18-19: precompute the constant parts, GPU adds W[11] or W[12]
        let w18_base = tail_w[2]
            .wrapping_add(msg_sigma1(w16))
            .wrapping_add(msg_sigma0(tail_w[3]));
        let w19_base = tail_w[3]
            .wrapping_add(msg_sigma1(w17))
            .wrapping_add(msg_sigma0(tail_w[4]));
        // Rounds 20-24: precompute constant portions (sigma0 of static words + static terms)
        // SCHED(20): W[4] += sigma1(W[2]) + W[13] + sigma0(W[5])
        //   constant part: sigma0(W[5]) + W[4]  (GPU adds: sigma1(W[2]) + W[13])
        let w20_base = tail_w[4].wrapping_add(msg_sigma0(tail_w[5]));
        // SCHED(21): W[5] += sigma1(W[3]) + W[14] + sigma0(W[6])
        //   constant part: sigma0(W[6]) + W[14] + W[5]  (GPU adds: sigma1(W[3]))
        let w21_base = tail_w[5].wrapping_add(tail_w[14]).wrapping_add(msg_sigma0(tail_w[6]));
        // SCHED(22): W[6] += sigma1(W[4]) + W[15] + sigma0(W[7])
        //   constant part: sigma0(W[7]) + W[15] + W[6]  (GPU adds: sigma1(W[4]))
        let w22_base = tail_w[6].wrapping_add(tail_w[15]).wrapping_add(msg_sigma0(tail_w[7]));
        // SCHED(23): W[7] += sigma1(W[5]) + W[0=w16] + sigma0(W[8])
        //   constant part: sigma0(W[8]) + W[0=w16] + W[7]  (GPU adds: sigma1(W[5]))
        let w23_base = tail_w[7].wrapping_add(w16).wrapping_add(msg_sigma0(tail_w[8]));
        // SCHED(24): W[8] += sigma1(W[6]) + W[1=w17] + sigma0(W[9])
        //   constant part: sigma0(W[9]) + W[1=w17] + W[8]  (GPU adds: sigma1(W[6]))
        let w24_base = tail_w[8].wrapping_add(w17).wrapping_add(msg_sigma0(tail_w[9]));

        let mut params = MiningParams {
            midstate: *midstate,
            tail_w,
            tail_len: tail.len() as u32,
            base_nonce_len: base_nonce_bytes.len() as u32,
            total_prefix_len,
            suffix: [0u8; 8],
            suffix_len: suffix.len() as u32,
            _pad1: 0,
            nonce_start: start_nonce,
            difficulty_bits: diff_bits as u32,
            num_precomputed_rounds: num_precomputed_rounds as u32,
            batch_size,
            base_nonce_str,
            _pad3: 0,
            partial_state,
            precomputed_w: [w16, w17, w18_base, w19_base, w20_base, w21_base, w22_base, w23_base, w24_base, 0, 0, 0],
        };
        params.suffix[..suffix.len()].copy_from_slice(suffix);

        let buf = &self.buffers[0];
        buf.reset_and_write_params(&params);

        let command_buffer = self.encode_and_submit(buf, batch_size);
        command_buffer.wait_until_completed();

        if command_buffer.status() == MTLCommandBufferStatus::Error {
            anyhow::bail!("Metal command buffer error");
        }

        Ok(buf.read_result())
    }
}
