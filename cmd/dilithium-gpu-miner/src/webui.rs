// Web UI server for monitoring dashboard

use axum::{
    extract::State,
    http::StatusCode,
    response::{Html, IntoResponse, Json},
    routing::get,
    Router,
};
use serde::{Deserialize, Serialize};
use std::sync::{
    atomic::{AtomicU64, Ordering},
    Arc,
};
use std::time::{Duration, Instant};
use tower_http::cors::CorsLayer;

#[derive(Clone)]
pub struct MinerStats {
    pub blocks_mined: Arc<AtomicU64>,
    pub total_hashes: Arc<AtomicU64>,
    pub total_earnings: Arc<AtomicU64>,
    pub current_hashrate: Arc<AtomicU64>, // Real-time hashrate in H/s
    pub start_time: Instant,
    pub wallet_address: String,
    pub node_url: String,
    pub device_id: i32,
    pub batch_size: u64,
}

#[derive(Serialize, Deserialize)]
pub struct StatsResponse {
    pub hashrate: f64,
    pub blocks_mined: u64,
    pub earnings: f64,
    pub total_hashes: u64,
    pub uptime_seconds: u64,
    pub wallet_address: String,
    pub node_url: String,
    pub device_id: i32,
    pub batch_size: u64,
}

async fn get_stats(State(stats): State<MinerStats>) -> Json<StatsResponse> {
    let elapsed = stats.start_time.elapsed().as_secs();
    let total_h = stats.total_hashes.load(Ordering::Relaxed);
    let blocks = stats.blocks_mined.load(Ordering::Relaxed);
    let earnings = stats.total_earnings.load(Ordering::Relaxed);

    // Use current real-time hashrate from atomic counter
    let hashrate = stats.current_hashrate.load(Ordering::Relaxed) as f64;

    Json(StatsResponse {
        hashrate,
        blocks_mined: blocks,
        earnings: earnings as f64 / 1e8,
        total_hashes: total_h,
        uptime_seconds: elapsed,
        wallet_address: stats.wallet_address.clone(),
        node_url: stats.node_url.clone(),
        device_id: stats.device_id,
        batch_size: stats.batch_size,
    })
}

async fn serve_dashboard() -> impl IntoResponse {
    Html(include_str!("../static/dashboard.html"))
}

pub async fn start_web_server(stats: MinerStats, port: u16) {
    let app = Router::new()
        .route("/", get(serve_dashboard))
        .route("/api/stats", get(get_stats))
        .layer(CorsLayer::permissive())
        .with_state(stats);

    let addr = format!("127.0.0.1:{}", port);
    log::info!("[*] Web dashboard available at http://{}", addr);

    let listener = tokio::net::TcpListener::bind(&addr)
        .await
        .expect("Failed to bind web server");

    axum::serve(listener, app)
        .await
        .expect("Web server failed");
}
