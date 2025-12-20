export interface SearchResult {
	file_path: string;
	chunk_index: number;
	content: string;
}

export interface SearchResponse {
	results: SearchResult[];
	query: string;
	answer?: string;
}

export interface SearchRequest {
	query: string;
	limit?: number;
}

export interface StatsResponse {
	total_chunks: number;
	total_files: number;
}

export interface HealthResponse {
	status: string;
	error?: string;
}
