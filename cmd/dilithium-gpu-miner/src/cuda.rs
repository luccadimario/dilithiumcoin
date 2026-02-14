// CUDA FFI bindings for GPU mining

use anyhow::Result;
use std::os::raw::{c_int, c_uchar, c_uint};

// Manual FFI bindings to CUDA bridge functions
extern "C" {
    fn gpu_init(device_id: c_int) -> c_int;
    fn gpu_cleanup();
    fn gpu_get_sm_count(device_id: c_int) -> c_int;
    fn gpu_mine_batch(
        midstate: *const c_uint,
        tail: *const c_uchar,
        tail_len: c_int,
        total_prefix_len: u64,
        suffix: *const c_uchar,
        suffix_len: c_int,
        diff_bits: c_int,
        start_nonce: u64,
        batch_size: u64,
        result_nonce: *mut u64,
        result_hash: *mut c_uchar,
    ) -> c_int;
}

pub struct GpuDevice {
    device_id: i32,
}

impl GpuDevice {
    pub fn new(device_id: i32) -> Result<Self> {
        unsafe {
            let ret = gpu_init(device_id);
            if ret != 0 {
                anyhow::bail!("Failed to initialize GPU device {}", device_id);
            }
        }

        Ok(Self { device_id })
    }

    pub fn get_sm_count(&self) -> i32 {
        unsafe { gpu_get_sm_count(self.device_id) }
    }

    pub fn mine_batch(
        &self,
        midstate: &[u32; 8],
        tail: &[u8],
        total_prefix_len: u64,
        suffix: &[u8],
        diff_bits: i32,
        start_nonce: u64,
        batch_size: u64,
    ) -> Result<Option<(u64, [u8; 32])>> {
        let mut result_nonce: u64 = 0;
        let mut result_hash: [u8; 32] = [0; 32];

        let ret = unsafe {
            gpu_mine_batch(
                midstate.as_ptr(),
                tail.as_ptr(),
                tail.len() as i32,
                total_prefix_len,
                suffix.as_ptr(),
                suffix.len() as i32,
                diff_bits,
                start_nonce,
                batch_size,
                &mut result_nonce,
                result_hash.as_mut_ptr(),
            )
        };

        match ret {
            1 => Ok(Some((result_nonce, result_hash))),
            0 => Ok(None),
            _ => anyhow::bail!("GPU mining error"),
        }
    }
}

impl Drop for GpuDevice {
    fn drop(&mut self) {
        unsafe {
            gpu_cleanup();
        }
    }
}
