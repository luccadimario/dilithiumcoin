export namespace main {
	
	export class BalanceInfo {
	    address: string;
	    balance_dlt: string;
	    total_received_dlt: string;
	    total_sent_dlt: string;
	    tx_count: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new BalanceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.balance_dlt = source["balance_dlt"];
	        this.total_received_dlt = source["total_received_dlt"];
	        this.total_sent_dlt = source["total_sent_dlt"];
	        this.tx_count = source["tx_count"];
	        this.error = source["error"];
	    }
	}
	export class NodeStatus {
	    connected: boolean;
	    version: string;
	    block_height: number;
	    pending_txs: number;
	    difficulty: number;
	    peer_count: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.version = source["version"];
	        this.block_height = source["block_height"];
	        this.pending_txs = source["pending_txs"];
	        this.difficulty = source["difficulty"];
	        this.peer_count = source["peer_count"];
	        this.error = source["error"];
	    }
	}
	export class TransactionInfo {
	    from: string;
	    to: string;
	    amount_dlt: string;
	    timestamp: number;
	    direction: string;
	
	    static createFrom(source: any = {}) {
	        return new TransactionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.amount_dlt = source["amount_dlt"];
	        this.timestamp = source["timestamp"];
	        this.direction = source["direction"];
	    }
	}
	export class TxResult {
	    success: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new TxResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	    }
	}
	export class WalletInfo {
	    address: string;
	    encrypted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WalletInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.encrypted = source["encrypted"];
	    }
	}

}

