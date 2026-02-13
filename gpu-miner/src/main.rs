mod cuda;
mod miner;
mod network;
mod sha256;
mod webui;
mod worker;

use anyhow::Result;
use clap::Parser;
use std::time::Instant;

#[derive(Parser, Debug)]
#[command(
    name = "dilithium-gpu-miner",
    about = "Dilithium GPU Miner - Rust+CUDA Implementation",
    version = "1.0.0"
)]
struct Args {
    /// Wallet address to mine to
    #[arg(short, long)]
    address: String,

    /// Node URL (default: http://localhost:8080)
    #[arg(short, long, default_value = "http://localhost:8080")]
    node: String,

    /// GPU device ID (default: 0)
    #[arg(short, long, default_value = "0")]
    device: i32,

    /// Batch size (number of nonces per kernel launch)
    #[arg(short, long, default_value = "67108864")]
    batch_size: u64,

    /// Web dashboard port (default: 8080)
    #[arg(short, long, default_value = "8080")]
    webui_port: u16,
}

#[tokio::main]
async fn main() -> Result<()> {
    // Initialize logger
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).init();

    // Parse arguments
    let args = Args::parse();

    log::info!("=======================================================");
    log::info!("   DILITHIUM GPU MINER - Rust+CUDA Implementation");
    log::info!("=======================================================");
    log::info!("");
    log::info!("[*] Wallet address: {}", args.address);
    log::info!("[*] Node URL: {}", args.node);
    log::info!("[*] GPU device: {}", args.device);
    log::info!("[*] Batch size: {}", args.batch_size);
    log::info!("[*] Web UI: http://127.0.0.1:{}", args.webui_port);
    log::info!("");

    // Create miner
    let miner = miner::Miner::new(
        args.node.clone(),
        args.address.clone(),
        args.device,
        args.batch_size,
    );

    // Track start time
    let start_time = Instant::now();

    // Get miner stats for web UI
    let stats = miner.get_stats(start_time);

    // Spawn web server
    let webui_handle = tokio::spawn(async move {
        webui::start_web_server(stats, args.webui_port).await;
    });

    // Setup Ctrl+C handler
    let miner_clone = miner.clone();
    ctrlc::set_handler(move || {
        log::info!("[*] Received shutdown signal, stopping...");
        miner_clone.stop();
    })?;

    // Run miner (blocks until stopped)
    let miner_result = miner.run().await;

    // Cleanup
    webui_handle.abort();
    log::info!("[*] Miner stopped");

    miner_result
}
