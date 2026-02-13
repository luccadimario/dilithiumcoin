// Dilithium node API client

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::time::Duration;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Transaction {
    pub from: String,
    pub to: String,
    pub amount: i64,
    pub timestamp: i64,
    pub signature: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Block {
    #[serde(rename = "Index")]
    pub index: i64,
    #[serde(rename = "Timestamp")]
    pub timestamp: i64,
    pub transactions: Vec<Transaction>,
    #[serde(rename = "PreviousHash")]
    pub previous_hash: String,
    #[serde(rename = "Hash")]
    pub hash: String,
    #[serde(rename = "Nonce")]
    pub nonce: i64,
    #[serde(rename = "Difficulty")]
    pub difficulty: i32,
    #[serde(rename = "DifficultyBits", skip_serializing_if = "Option::is_none")]
    pub difficulty_bits: Option<i32>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BlockTemplate {
    #[serde(rename = "Index")]
    pub index: i64,
    #[serde(rename = "Height")]
    pub height: i64,
    #[serde(rename = "PreviousHash")]
    pub previous_hash: String,
    #[serde(rename = "Difficulty")]
    pub difficulty: i32,
    #[serde(rename = "DifficultyBits")]
    pub difficulty_bits: i32,
    #[serde(rename = "Reward")]
    pub reward: i64,
}

pub struct NodeClient {
    base_url: String,
    client: reqwest::Client,
}

impl NodeClient {
    pub fn new(base_url: String) -> Self {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .expect("Failed to create HTTP client");

        Self { base_url, client }
    }

    pub async fn get_work(&self) -> Result<BlockTemplate> {
        // Get node status for current chain tip and difficulty
        let status_url = format!("{}/status", self.base_url);
        let status_response = self
            .client
            .get(&status_url)
            .send()
            .await
            .context("Failed to get node status")?;

        let status_json: serde_json::Value = status_response
            .json()
            .await
            .context("Failed to parse status response")?;

        let data = status_json
            .get("data")
            .context("Missing data in status")?;

        let current_height = data
            .get("blockchain_height")
            .and_then(|h| h.as_i64())
            .context("Missing blockchain_height in status")?;

        let last_block_hash = data
            .get("last_block_hash")
            .and_then(|h| h.as_str())
            .context("Missing last_block_hash in status")?
            .to_string();

        let difficulty = data
            .get("difficulty")
            .and_then(|d| d.as_i64())
            .context("Missing difficulty in status")? as i32;

        let difficulty_bits = data
            .get("difficulty_bits")
            .and_then(|d| d.as_i64())
            .context("Missing difficulty_bits in status")? as i32;

        // Create block template for next block
        // Note: blockchain_height is the count of blocks (e.g., 5880 means blocks 0-5879)
        // So blockchain_height is already the next block index

        // Calculate block reward with halving (same as Dilithium network)
        let reward = Self::calculate_block_reward(current_height);

        Ok(BlockTemplate {
            index: current_height,
            height: current_height - 1,
            previous_hash: last_block_hash,
            difficulty,
            difficulty_bits,
            reward,
        })
    }

    fn calculate_block_reward(block_height: i64) -> i64 {
        const INITIAL_BLOCK_REWARD: i64 = 50_00000000; // 50 DLT in satoshis
        const HALVING_INTERVAL: i64 = 250000;
        const MIN_BLOCK_REWARD: i64 = 1; // 1 base unit

        // Calculate number of halvings
        let halvings = block_height / HALVING_INTERVAL;

        // After 64 halvings, reward becomes zero
        if halvings >= 64 {
            return 0;
        }

        // Calculate reward: InitialReward / (2^halvings)
        let mut reward = INITIAL_BLOCK_REWARD;
        for _ in 0..halvings {
            reward /= 2;
        }

        // Don't go below minimum
        if reward < MIN_BLOCK_REWARD {
            reward = MIN_BLOCK_REWARD;
        }

        reward
    }

    pub async fn submit_block(&self, block: &Block) -> Result<()> {
        let url = format!("{}/block/submit", self.base_url);
        let response = self
            .client
            .post(&url)
            .json(block)
            .send()
            .await
            .context("Failed to submit block")?;

        if !response.status().is_success() {
            let text = response.text().await.unwrap_or_default();
            anyhow::bail!("Block rejected: {}", text);
        }

        Ok(())
    }

    pub async fn get_current_height(&self) -> Result<i64> {
        let url = format!("{}/status", self.base_url);
        let response = self
            .client
            .get(&url)
            .send()
            .await
            .context("Failed to get node status")?;

        if !response.status().is_success() {
            anyhow::bail!("Failed to get node status");
        }

        let response_json: serde_json::Value = response
            .json()
            .await
            .context("Failed to parse status response")?;

        let height = response_json
            .get("data")
            .and_then(|d| d.get("blockchain_height"))
            .and_then(|h| h.as_i64())
            .context("Missing blockchain_height in status")?;

        Ok(height)
    }

    pub async fn get_pending_transactions(&self) -> Result<Vec<Transaction>> {
        let url = format!("{}/mempool", self.base_url);
        let response = self.client.get(&url).send().await?;

        if !response.status().is_success() {
            return Ok(Vec::new());
        }

        response.json().await.context("Failed to parse transactions")
    }

    pub async fn check_connection(&self) -> Result<()> {
        let url = format!("{}/chain", self.base_url);
        self.client
            .get(&url)
            .send()
            .await
            .context("Failed to connect to node")?;
        Ok(())
    }
}
