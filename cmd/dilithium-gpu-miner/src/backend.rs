// GPU backend abstraction layer

use anyhow::Result;

pub struct MiningResult {
    pub nonce: u64,
    pub hash: [u8; 32],
}

/// Trait for GPU mining backends (CUDA, Metal, etc.)
pub trait GpuBackend: Send + Sync {
    /// Human-readable name of the backend (e.g. "CUDA", "Metal")
    fn name(&self) -> &str;

    /// Number of compute units (SMs for CUDA, execution units for Metal)
    fn compute_unit_count(&self) -> i32;

    /// Mine a batch of nonces on the GPU.
    fn mine_batch(
        &self,
        midstate: &[u32; 8],
        tail: &[u8],
        total_prefix_len: u64,
        suffix: &[u8],
        diff_bits: i32,
        start_nonce: u64,
        batch_size: u64,
    ) -> Result<Option<(u64, [u8; 32])>>;
}

/// Create the appropriate GPU backend for the current platform and features.
pub fn create_backend(device_id: i32) -> Result<Box<dyn GpuBackend>> {
    #[cfg(feature = "metal")]
    {
        let backend = crate::metal_backend::MetalBackend::new(device_id)?;
        return Ok(Box::new(backend));
    }

    #[cfg(feature = "cuda")]
    {
        let backend = crate::cuda::CudaBackend::new(device_id)?;
        return Ok(Box::new(backend));
    }

    #[cfg(not(any(feature = "cuda", feature = "metal")))]
    {
        let _ = device_id;
        anyhow::bail!(
            "No GPU backend available. Build with --features cuda or --features metal"
        );
    }
}
